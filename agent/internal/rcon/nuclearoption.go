// nuclearoption.go implements the Nuclear Option dedicated server's remote-command
// protocol (length-prefixed JSON over TCP).
//
// Protocol reference: specs/002-nuclear-option-ip-pool/contracts/nuclear-option-remote-command.md
//
// Wire format (TCP):
//
//	Request:  4-byte LE int32 length (JSON body bytes only) + UTF-8 JSON body
//	Response: 4-byte LE int32 status + 4-byte LE int32 body length + body (if length > 0)
//
// Request body shape: {"name":"command-name","arguments":["arg0","arg1",...]}
//
// Status codes: 2000 success, 4000-4005 client errors, 5000-5002 server errors.
// Response body is present only when body length > 0. Status 2000 is success;
// non-2000 is an error. When the body is present (length > 0), it may carry
// a "message" field with error detail.
//
// Command-line parsing rule: The input command line is split on the FIRST space
// to extract the command name. The remainder is then split into arguments by
// space, EXCEPT for send-chat-message, which is a special case: for this command,
// the entire remainder after the command name is passed as a single argument
// (preserving spaces in the message). Other commands split their arguments by space.
// Example: "send-chat-message hello world" -> ["hello world"]
// Example: "kick-player 76561198123456789" -> ["76561198123456789"]

package rcon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	defaultNuclearOptionPort = 7779

	// defaultNuclearOptionDialTimeout bounds the underlying TCP connect.
	// A struct field (not just this const) so tests can shrink it.
	defaultNuclearOptionDialTimeout = 5 * time.Second

	// defaultNuclearOptionExecDeadline bounds one whole roundtrip
	// (connect + request + response). A struct field so tests can shrink it.
	defaultNuclearOptionExecDeadline = 30 * time.Second

	// nuclearOptionMaxBodyLength bounds the response body to prevent unbounded
	// allocation. 10 MiB is conservative for typical mission/player-list payloads.
	nuclearOptionMaxBodyLength = 10 << 20 // 10 MiB

	// statusSuccess is the only non-error status code.
	statusSuccess = 2000
)

// nucleaOptionRequest is the wire-format request envelope.
type nuclearOptionRequest struct {
	Name      string   `json:"name"`
	Arguments []string `json:"arguments"`
}

// nuclearOptionErrorResponse carries error detail when present in the body.
type nuclearOptionErrorResponse struct {
	Message string `json:"message,omitempty"`
}

// NuclearOption is a TCP client for the Nuclear Option remote-command protocol.
// It's safe for use from multiple goroutines; all ops are serialized on a single conn.
type NuclearOption struct {
	host string
	port int

	// dialTimeout/execDeadline are struct fields (not package consts) so
	// tests can shrink them.
	dialTimeout  time.Duration
	execDeadline time.Duration

	mu   sync.Mutex
	conn net.Conn
}

// NewNuclearOption builds a Nuclear Option remote-command client.
func NewNuclearOption(host string, port int, _ PassFn) *NuclearOption {
	if port == 0 {
		port = defaultNuclearOptionPort
	}
	return &NuclearOption{
		host:         host,
		port:         port,
		dialTimeout:  defaultNuclearOptionDialTimeout,
		execDeadline: defaultNuclearOptionExecDeadline,
	}
}

// Close shuts down the underlying connection.
func (c *NuclearOption) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// Exec runs one remote command and returns the response body as a string.
// If the response has no body (body length = 0), an empty string is returned.
// Non-2000 status codes produce an error that includes the status code and,
// if present, the error detail from the response body.
// One deadline bounds the complete exchange, including dialing and request writes.
func (c *NuclearOption) Exec(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	d := c.execDeadline
	if d <= 0 {
		d = defaultNuclearOptionExecDeadline
	}
	exchangeDeadline := time.Now().Add(d)

	// Ensure connection is open.
	if c.conn == nil {
		d := net.Dialer{Timeout: c.dialTimeout}
		ctx, cancel := context.WithDeadline(context.Background(), exchangeDeadline)
		defer cancel()
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.host, fmt.Sprint(c.port)))
		if err != nil {
			return "", fmt.Errorf("nuclearoption rcon: dial: %w", err)
		}
		c.conn = conn
	}

	// Parse the command line into name and arguments.
	name, args := parseNuclearOptionCommand(cmd)
	if name == "" {
		return "", fmt.Errorf("nuclearoption rcon: empty command")
	}

	// Encode the request.
	req := nuclearOptionRequest{
		Name:      name,
		Arguments: args,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("nuclearoption rcon: marshal request: %w", err)
	}

	// Bound writes as well as reads. Arm this before sending anything: a
	// server that stops reading must not retain the shared client forever.
	if err := c.conn.SetDeadline(exchangeDeadline); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return "", fmt.Errorf("nuclearoption rcon: set deadline: %w", err)
	}

	// Write request: 4-byte LE length + body.
	reqLen := len(reqBody)
	if reqLen < 0 || reqLen > math.MaxUint32 {
		return "", fmt.Errorf("nuclearoption rcon: request body too large (%d bytes)", reqLen)
	}
	lengthBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lengthBuf, uint32(reqLen))
	if _, err := c.conn.Write(lengthBuf); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return "", fmt.Errorf("nuclearoption rcon: write length: %w", err)
	}
	if _, err := c.conn.Write(reqBody); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return "", fmt.Errorf("nuclearoption rcon: write body: %w", err)
	}

	// Read response: 4-byte status + 4-byte body length + body.
	statusBuf := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, statusBuf); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return "", fmt.Errorf("nuclearoption rcon: read status: %w", err)
	}
	status := binary.LittleEndian.Uint32(statusBuf)

	lengthBuf = make([]byte, 4)
	if _, err := io.ReadFull(c.conn, lengthBuf); err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return "", fmt.Errorf("nuclearoption rcon: read body length: %w", err)
	}
	bodyLen := binary.LittleEndian.Uint32(lengthBuf)

	// Bound body length to prevent unbounded allocation.
	if bodyLen > nuclearOptionMaxBodyLength {
		_ = c.conn.Close()
		c.conn = nil
		return "", fmt.Errorf("nuclearoption rcon: response body too large (%d bytes)", bodyLen)
	}

	var body []byte
	if bodyLen > 0 {
		body = make([]byte, bodyLen)
		if _, err := io.ReadFull(c.conn, body); err != nil {
			_ = c.conn.Close()
			c.conn = nil
			return "", fmt.Errorf("nuclearoption rcon: read body: %w", err)
		}
	}

	// Check status code. 2000 is success; all others are errors.
	if status != statusSuccess {
		// Extract error message from body if present.
		errMsg := ""
		if len(body) > 0 {
			var errResp nuclearOptionErrorResponse
			if uerr := json.Unmarshal(body, &errResp); uerr == nil && errResp.Message != "" {
				errMsg = fmt.Sprintf(": %s", errResp.Message)
			}
		}
		return "", fmt.Errorf("nuclearoption rcon: status %d%s", status, errMsg)
	}

	// Status 2000: return the body (as string, even if empty).
	return string(body), nil
}

// parseNuclearOptionCommand splits the input command line into a command name
// and arguments. The command name is the first space-separated word.
//
// The remainder is parsed into arguments as follows. For "send-chat-message",
// the entire remainder (after the command name) is passed as a single
// argument, preserving spaces. For all other commands, the remainder is
// split by space into arguments.
//
// This prevents truncation of the message in send-chat-message commands.
func parseNuclearOptionCommand(cmd string) (name string, args []string) {
	// Trim only leading whitespace to preserve trailing spaces in arguments.
	cmd = strings.TrimLeft(cmd, " \t\n\r")
	if cmd == "" {
		return "", nil
	}

	// Split on first space to get the command name.
	parts := strings.SplitN(cmd, " ", 2)
	name = parts[0]
	if name == "" {
		return "", nil
	}

	// Handle arguments.
	if len(parts) == 1 {
		// No remainder; command takes no arguments.
		return name, nil
	}

	remainder := parts[1]

	// Special case: send-chat-message takes a single argument (the message),
	// which may contain spaces and should not be split.
	if strings.EqualFold(name, "send-chat-message") {
		// Trim trailing whitespace from the message.
		remainder = strings.TrimRight(remainder, " \t\n\r")
		return name, []string{remainder}
	}

	// For all other commands, split the remainder by space.
	// Filter out empty strings (in case there are multiple consecutive spaces).
	var splitArgs []string
	for _, arg := range strings.Fields(remainder) {
		if arg != "" {
			splitArgs = append(splitArgs, arg)
		}
	}
	return name, splitArgs
}

var (
	_ interface{ Exec(string) (string, error) } = (*NuclearOption)(nil)
)

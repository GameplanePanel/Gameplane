// Package a2s reads a game server's player counts with Valve's A2S_INFO
// query, which most Steam dedicated servers answer on their query port.
//
// Protocol reference: https://developer.valvesoftware.com/wiki/Server_queries
//
// Wire format (UDP, one datagram each way):
//
//	request:  FF FF FF FF | 'T'(0x54) | "Source Engine Query\x00" [| 4-byte challenge]
//	reply:    FF FF FF FF | 'I'(0x49) | protocol byte | name\x00 | map\x00 |
//	          folder\x00 | game\x00 | app ID (2 bytes, little-endian) |
//	          players byte | max players byte | bots byte | ...
//
// A server may answer the first request with a challenge instead,
// FF FF FF FF | 'A'(0x41) | 4-byte challenge, and then expects the request
// again with the challenge appended. Only the single-packet reply is
// supported: A2S_INFO replies fit in one datagram in practice, and a
// split reply (FE FF FF FF) is reported as an error.
package a2s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	headerInfo      = 0x49
	headerChallenge = 0x41
	// Replies are at most one datagram; 1400 bytes is the protocol's
	// maximum single-packet size, with headroom.
	maxPacket = 4096
	// A server answers at most this many challenges before we give up,
	// so a server that keeps issuing new ones can't hold the query open.
	maxChallenges = 2
)

var (
	singlePacket = []byte{0xFF, 0xFF, 0xFF, 0xFF}
	infoRequest  = append(append([]byte{}, singlePacket...), append([]byte{'T'}, "Source Engine Query\x00"...)...)
)

// Info is the part of an A2S_INFO reply that the agent reports.
type Info struct {
	// Players is the number of players online, bots included.
	Players int
	// MaxPlayers is the server's player limit.
	MaxPlayers int
	// Bots is the number of those players that are bots.
	Bots int
}

// Humans is the number of online players who aren't bots.
func (i Info) Humans() int {
	if i.Bots >= i.Players {
		return 0
	}
	return i.Players - i.Bots
}

// QueryInfo sends A2S_INFO to addr (host:port) and parses the reply. The
// timeout bounds the whole exchange, challenge round trips included; ctx
// bounds the dial.
func QueryInfo(ctx context.Context, addr string, timeout time.Duration) (Info, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return Info{}, fmt.Errorf("a2s dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return Info{}, fmt.Errorf("a2s deadline: %w", err)
	}

	req := infoRequest
	buf := make([]byte, maxPacket)
	for range maxChallenges + 1 {
		if _, err := conn.Write(req); err != nil {
			return Info{}, fmt.Errorf("a2s write: %w", err)
		}
		n, err := conn.Read(buf)
		if err != nil {
			return Info{}, fmt.Errorf("a2s read: %w", err)
		}
		challenge, info, err := parseReply(buf[:n])
		if err != nil {
			return Info{}, err
		}
		if challenge == nil {
			return info, nil
		}
		req = append(append([]byte{}, infoRequest...), challenge...)
	}
	return Info{}, errors.New("a2s: server kept answering with a challenge")
}

// parseReply returns either the challenge to resend the request with, or
// the parsed info.
func parseReply(b []byte) (challenge []byte, info Info, err error) {
	if len(b) < 5 || !bytes.Equal(b[:4], singlePacket) {
		return nil, Info{}, errors.New("a2s: not a single-packet reply")
	}
	switch b[4] {
	case headerChallenge:
		if len(b) < 9 {
			return nil, Info{}, errors.New("a2s: short challenge")
		}
		return append([]byte{}, b[5:9]...), Info{}, nil
	case headerInfo:
		info, err := parseInfo(b[5:])
		return nil, info, err
	default:
		return nil, Info{}, fmt.Errorf("a2s: unexpected reply header 0x%02x", b[4])
	}
}

// parseInfo parses an A2S_INFO payload, after its 0x49 header.
func parseInfo(b []byte) (Info, error) {
	// Protocol version byte.
	if len(b) < 1 {
		return Info{}, errors.New("a2s: short info reply")
	}
	b = b[1:]
	// Name, map, folder and game.
	for range 4 {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			return Info{}, errors.New("a2s: truncated string in info reply")
		}
		b = b[i+1:]
	}
	// App ID (2 bytes), then players, max players and bots.
	if len(b) < 5 {
		return Info{}, errors.New("a2s: short info reply")
	}
	return Info{Players: int(b[2]), MaxPlayers: int(b[3]), Bots: int(b[4])}, nil
}

package rcon

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestExecContinuousFragmentsRespectAbsoluteDeadline(t *testing.T) {
	t.Parallel()
	for _, unrelated := range []bool{false, true} {
		name := "matching"
		if unrelated {
			name = "unrelated-after-response"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			client := &Client{conn: clientConn, execDeadline: 100 * time.Millisecond}
			server := &Client{conn: serverConn}
			done := make(chan struct{})
			go func() {
				defer close(done)
				id, _, _, err := server.readPacket()
				if err != nil {
					return
				}
				if server.writePacket(id, typeRespValue, "partial") != nil {
					return
				}
				if unrelated {
					id++
				}
				// Empty fragments cannot trigger the aggregate byte cap. A
				// continuously readable socket must still reach its hard deadline.
				for {
					if server.writePacket(id, typeRespValue, "") != nil {
						return
					}
				}
			}()
			finished := make(chan struct{})
			var output string
			var execErr error
			go func() {
				output, execErr = client.Exec("status")
				close(finished)
			}()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				// Unblock both goroutines even when testing the old read loop.
				_ = serverConn.Close()
				<-finished
				t.Fatal("continuous fragments extended the absolute command deadline")
			}
			if execErr == nil || !isTimeout(execErr) || output != "" {
				t.Fatalf("deadline must discard incomplete output: bytes=%d err=%v", len(output), execErr)
			}
			if client.conn != nil {
				t.Fatal("deadline must drop the connection to discard trailing fragments")
			}
			<-done
		})
	}
}

func TestExecAggregateResponseBound(t *testing.T) {
	t.Parallel()
	for _, extra := range []int{0, 1} {
		name := "exact-limit"
		if extra > 0 {
			name = "over-limit"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			client := &Client{conn: clientConn, execDeadline: 10 * time.Second}
			server := &Client{conn: serverConn}
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer serverConn.Close()
				id, _, _, err := server.readPacket()
				if err != nil {
					return
				}
				fragment := strings.Repeat("x", 4000)
				remaining := sourceMaxResponseBytes + extra
				for remaining > 0 {
					length := min(remaining, len(fragment))
					if server.writePacket(id, typeRespValue, fragment[:length]) != nil {
						return
					}
					remaining -= length
				}
			}()
			output, err := client.Exec("large-list")
			<-done
			if extra == 0 {
				if err != nil || len(output) != sourceMaxResponseBytes || strings.Trim(output, "x") != "" {
					t.Fatalf("legitimate response at the generous cap must succeed: bytes=%d err=%v", len(output), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "response exceeded limit") || output != "" {
				t.Fatalf("oversized response must fail without a truncated result: bytes=%d err=%v", len(output), err)
			}
			if client.conn != nil {
				t.Fatal("oversized response must drop the connection")
			}
		})
	}
}

func TestAuthContinuousPreambleRespectsAbsoluteDeadline(t *testing.T) {
	t.Parallel()
	lc := &net.ListenConfig{}
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Bound the fixture itself even if the client's deadline regresses.
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		server := &Client{conn: conn}
		id, _, _, err := server.readPacket()
		if err != nil {
			return
		}
		for {
			if server.writePacket(id, typeRespValue, "") != nil {
				return
			}
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := New(host, mustAtoi(port), func() (string, error) { return "pw", nil })
	client.authDeadline = 100 * time.Millisecond
	defer client.Close()
	started := time.Now()
	output, err := client.Exec("status")
	if err == nil || !isTimeout(err) || output != "" || time.Since(started) > 2*time.Second {
		t.Fatalf("continuous auth preamble must time out without output: bytes=%d err=%v elapsed=%v", len(output), err, time.Since(started))
	}
	if client.conn != nil {
		t.Fatal("authentication deadline must drop the connection")
	}
	<-done
}

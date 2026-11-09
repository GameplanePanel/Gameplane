package rcon

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNuclearOptionDeadlineIncludesRequestWrites(t *testing.T) {
	t.Parallel()
	for _, readHeader := range []bool{false, true} {
		stage := "write length"
		if readHeader {
			stage = "write body"
		}
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()
			client := NewNuclearOption("ignored", 1, nil)
			client.conn = local
			client.execDeadline = 100 * time.Millisecond
			if readHeader {
				go func() { _, _ = io.CopyN(io.Discard, remote, 4) }()
			}
			done := make(chan error, 1)
			go func() { _, err := client.Exec("get-server-id"); done <- err }()
			select {
			case err := <-done:
				if err == nil || !isTimeout(err) || !strings.Contains(err.Error(), stage) {
					t.Fatalf("stalled %s must time out: %v", stage, err)
				}
			case <-time.After(2 * time.Second):
				_ = remote.Close()
				<-done
				t.Fatalf("stalled %s exceeded the command budget", stage)
			}
			if client.conn != nil {
				t.Fatal("timed-out partial request must discard the connection")
			}
		})
	}
}

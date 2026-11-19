package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

func TestJoinOnceNeedsWorldData(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply []byte
		want  joindepth.JoinDepth
	}{
		{"password", []byte{3, 0, 37}, joindepth.PARTIAL},
		{"only slot", []byte{4, 0, 3, 5}, joindepth.PARTIAL},
		{"world", []byte{4, 0, 3, 5, 4, 0, 7, 1}, joindepth.JOINED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			go func() {
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Second))
				var length [2]byte
				if _, err := io.ReadFull(c, length[:]); err != nil {
					return
				}
				if _, err := io.CopyN(io.Discard, c, int64(length[0])-2); err != nil {
					return
				}
				_, _ = c.Write(tc.reply)
				if len(tc.reply) > 4 {
					var request [3]byte
					_, _ = io.ReadFull(c, request[:])
				}
			}()
			v := join(ctx, "terraria", l.Addr().String(), "gp")
			if v.ReachedDepth != tc.want || (tc.want == joindepth.JOINED && (v.Err != nil || v.Detail == "")) {
				t.Fatalf("join = %+v, want %v", v, tc.want)
			}
		})
	}
}

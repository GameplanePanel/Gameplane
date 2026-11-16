package a2s

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

// infoReply builds an A2S_INFO reply with the given counts.
func infoReply(players, maxPlayers, bots byte) []byte {
	b := append([]byte{}, singlePacket...)
	b = append(b, headerInfo, 17)
	b = append(b, "My Server\x00Dedicated\x00valheim\x00Valheim\x00"...)
	b = append(b, 0x00, 0x00) // app ID
	return append(b, players, maxPlayers, bots, 'd', 'l', 0, 1)
}

// serve answers each request on a loopback UDP socket with reply(request).
func serve(t *testing.T, reply func(req []byte) []byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, maxPacket)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if r := reply(append([]byte{}, buf[:n]...)); r != nil {
				_, _ = pc.WriteTo(r, addr)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func TestQueryInfo(t *testing.T) {
	addr := serve(t, func(req []byte) []byte {
		if !bytes.Equal(req, infoRequest) {
			t.Errorf("request = %q, want %q", req, infoRequest)
		}
		return infoReply(3, 10, 1)
	})
	info, err := QueryInfo(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info != (Info{Players: 3, MaxPlayers: 10, Bots: 1}) {
		t.Errorf("info = %+v", info)
	}
	if info.Humans() != 2 {
		t.Errorf("Humans() = %d, want 2", info.Humans())
	}
}

func TestQueryInfoChallenge(t *testing.T) {
	challenge := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	addr := serve(t, func(req []byte) []byte {
		if bytes.HasSuffix(req, challenge) {
			return infoReply(0, 10, 0)
		}
		return append(append(append([]byte{}, singlePacket...), headerChallenge), challenge...)
	})
	info, err := QueryInfo(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info.Players != 0 || info.MaxPlayers != 10 {
		t.Errorf("info = %+v", info)
	}
}

func TestQueryInfoEndlessChallenge(t *testing.T) {
	addr := serve(t, func([]byte) []byte {
		return append(append([]byte{}, singlePacket...), headerChallenge, 1, 2, 3, 4)
	})
	if _, err := QueryInfo(addr, time.Second); err == nil || !strings.Contains(err.Error(), "challenge") {
		t.Errorf("err = %v, want a challenge error", err)
	}
}

func TestQueryInfoTimeout(t *testing.T) {
	addr := serve(t, func([]byte) []byte { return nil })
	if _, err := QueryInfo(addr, 100*time.Millisecond); err == nil {
		t.Error("want a timeout error")
	}
}

func TestParseReplyErrors(t *testing.T) {
	cases := map[string][]byte{
		"empty":            nil,
		"split packet":     {0xFE, 0xFF, 0xFF, 0xFF, headerInfo},
		"unknown header":   {0xFF, 0xFF, 0xFF, 0xFF, 0x6D},
		"short challenge":  {0xFF, 0xFF, 0xFF, 0xFF, headerChallenge, 1},
		"no protocol":      {0xFF, 0xFF, 0xFF, 0xFF, headerInfo},
		"truncated string": append([]byte{0xFF, 0xFF, 0xFF, 0xFF, headerInfo, 17}, "name"...),
		"no counts":        append([]byte{0xFF, 0xFF, 0xFF, 0xFF, headerInfo, 17}, "a\x00b\x00c\x00d\x00\x00"...),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseReply(b); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestHumansNeverNegative(t *testing.T) {
	if got := (Info{Players: 1, Bots: 2}).Humans(); got != 0 {
		t.Errorf("Humans() = %d, want 0", got)
	}
}

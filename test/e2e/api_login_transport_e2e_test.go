//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

// This classification uses no cluster requests or login budget.
func TestAPI_LoginTransportRefusalClassification(t *testing.T) {
	t.Parallel()
	dialRefused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"dial refused", dialRefused, true},
		{"wrapped dial refused", fmt.Errorf("login: %w", &url.Error{Op: "Post", URL: "http://127.0.0.1/auth/login", Err: dialRefused}), true},
		{"nil", nil, false},
		{"bare refusal", syscall.ECONNREFUSED, false},
		{"untyped text", errors.New("dial tcp: connection refused"), false},
		{"read refusal", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNREFUSED}, false},
		{"write refusal", &net.OpError{Op: "write", Net: "tcp", Err: syscall.ECONNREFUSED}, false},
		{"dial reset", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNRESET}, false},
		{"read reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, false},
		{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ETIMEDOUT}, false},
		{"deadline", &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}, false},
		{"canceled", context.Canceled, false},
		{"EOF", io.EOF, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := loginDialRefused(tc.err); got != tc.want {
				t.Fatalf("loginDialRefused(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

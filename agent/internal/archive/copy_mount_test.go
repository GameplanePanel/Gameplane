package archive

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
)

func TestMountPointRefused(t *testing.T) {
	eperm := fmt.Errorf("chmodat .: %w", syscall.EPERM)
	cases := []struct {
		name string
		path string
		err  error
		want bool
	}{
		{name: "mount point refused", path: ".", err: eperm, want: true},
		{name: "mount point access denied", path: ".", err: syscall.EACCES, want: true},
		{name: "subdirectory refused", path: "sub", err: eperm, want: false},
		{name: "mount point other error", path: ".", err: errors.New("disk full"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mountPointRefused(tc.path, tc.err); got != tc.want {
				t.Errorf("mountPointRefused(%q, %v) = %v, want %v", tc.path, tc.err, got, tc.want)
			}
		})
	}
}

package controller

import (
	"strings"
	"testing"
)

func TestBackupJobName(t *testing.T) {
	short := strings.Repeat("a", 63)
	if got := backupJobName(short); got != short {
		t.Fatalf("63-char name changed: %q", got)
	}
	long := strings.Repeat("b", 40) + "-auto-20260101-000000-extra-suffix-long"
	got := backupJobName(long)
	if len(got) > 63 {
		t.Fatalf("len = %d, want <= 63", len(got))
	}
	if got != backupJobName(long) {
		t.Fatal("not deterministic")
	}
	other := long + "x"
	if backupJobName(other) == got {
		t.Fatal("distinct long names collided")
	}
	if strings.HasSuffix(strings.SplitN(got, "-", 2)[0], ".") {
		t.Fatalf("bad prefix %q", got)
	}
}

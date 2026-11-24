package audit

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRedactURLErr(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://hook.example/secret-token", Err: errors.New("boom")}
	got := redactURLErr(err)
	if strings.Contains(got.Error(), "secret-token") {
		t.Errorf("redacted error still carries the URL: %q", got.Error())
	}
	if got.Error() != "boom" {
		t.Errorf("redactURLErr = %q, want %q", got.Error(), "boom")
	}

	plain := errors.New("plain failure")
	if redactURLErr(plain) != plain {
		t.Errorf("non-URL error was not passed through unchanged")
	}
}

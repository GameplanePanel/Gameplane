package telemetry

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestWithoutURL(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://collector.example/ingest?key=secret-token", Err: errors.New("boom")}
	got := withoutURL(err)
	if strings.Contains(got.Error(), "secret-token") {
		t.Errorf("stripped error still carries the URL: %q", got.Error())
	}
	if got.Error() != "boom" {
		t.Errorf("withoutURL = %q, want %q", got.Error(), "boom")
	}

	plain := errors.New("plain failure")
	if !errors.Is(withoutURL(plain), plain) {
		t.Errorf("non-URL error was not passed through unchanged")
	}
}

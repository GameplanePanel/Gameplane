package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// genericCodes are the HTTP statuses that have a server.generic.<code> entry.
var genericCodes = []int{400, 401, 403, 404, 409, 413, 422, 429, 500, 501, 502, 503}

func TestKnownIDsAreRegistered(t *testing.T) {
	for _, id := range httperr.KnownIDs() {
		if _, ok := Lookup(ID(id)); !ok {
			t.Errorf("httperr id %q is not registered", id)
		}
	}
}

func TestGenericIDsAreRegistered(t *testing.T) {
	for _, code := range genericCodes {
		id := ID(httperr.GenericID(code))
		e, ok := Lookup(id)
		if !ok {
			t.Errorf("generic id %q is not registered", id)
			continue
		}
		if e.Status != code {
			t.Errorf("%q status = %d, want %d", id, e.Status, code)
		}
	}
}

func TestNewRendersEnglishWithArgs(t *testing.T) {
	m := New(ID(httperr.IDInvalid), map[string]any{"detail": "name must be lowercase"})
	if m.English != "name must be lowercase" {
		t.Fatalf("English = %q", m.English)
	}
	if m.ID != httperr.IDInvalid {
		t.Fatalf("ID = %q", m.ID)
	}
	if m.Status != http.StatusUnprocessableEntity {
		t.Fatalf("Status = %d", m.Status)
	}
	if m.Args["detail"] != "name must be lowercase" {
		t.Fatalf("Args = %v", m.Args)
	}
}

func TestNewPlainEntryStatus(t *testing.T) {
	m := New(ID(httperr.IDNotFound), nil)
	if m.English != "not found" || m.Status != http.StatusNotFound {
		t.Fatalf("got English=%q Status=%d", m.English, m.Status)
	}
}

func TestNewUnknownIDIs500(t *testing.T) {
	m := New(ID("server.does_not_exist"), nil)
	if m.Status != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want 500", m.Status)
	}
	if m.English != "internal error" {
		t.Fatalf("English = %q", m.English)
	}
	if m.ID != "server.does_not_exist" {
		t.Fatalf("ID = %q", m.ID)
	}
}

func TestAllSortedAndUnique(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("registry is empty")
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID >= all[i].ID {
			t.Fatalf("not strictly sorted at %d: %q then %q", i, all[i-1].ID, all[i].ID)
		}
	}
}

func TestAllReturnsCopy(t *testing.T) {
	all := All()
	all[0].English = "mutated"
	if All()[0].English == "mutated" {
		t.Fatal("All must return a copy")
	}
}

func TestEveryEntryHasDescription(t *testing.T) {
	for _, e := range All() {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%q has no description", e.ID)
		}
	}
}

func TestRemoteClusterEnglishIsSafeICU(t *testing.T) {
	e, ok := Lookup(ID(httperr.IDRemoteClusterNotImplemented))
	if !ok {
		t.Fatal("remote cluster id not registered")
	}
	if e.English != httperr.RemoteClusterNotImplemented {
		t.Fatalf("English = %q", e.English)
	}
	if e.Status != http.StatusNotImplemented {
		t.Fatalf("Status = %d", e.Status)
	}
}

func TestExportJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportJSON(&buf); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "]\n") {
		t.Fatalf("export must end with a newline after the array: %q", buf.String()[len(buf.String())-5:])
	}
	var got []Entry
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := All()
	if len(got) != len(want) {
		t.Fatalf("decoded %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].English != want[i].English || got[i].Description != want[i].Description {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestExportJSONDoesNotEscapeHTML(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportJSON(&buf); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	if strings.Contains(buf.String(), `<`) || strings.Contains(buf.String(), `&`) {
		t.Fatal("export escapes HTML characters")
	}
}

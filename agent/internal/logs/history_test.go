package logs

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 110)), 0600); err != nil {
		t.Fatal(err)
	}
	base := mountServer(t, dir, path)
	request := func(endpoint, cursor string, status int) historyPage {
		t.Helper()
		resp, err := testGet(t, endpoint+"/logs/history?before="+url.QueryEscape(cursor))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("status=%d want=%d", resp.StatusCode, status)
		}
		var page historyPage
		if status == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
				t.Fatal(err)
			}
		}
		return page
	}
	page := request(base, "", 200)
	if strings.Count(page.Text, "\n") != 100 || page.Before == "" {
		t.Fatalf("page=%+v", page)
	}
	older := request(base, page.Before, 200)
	if strings.Count(older.Text, "\n") != 10 || older.Before != "" {
		t.Fatalf("older=%+v", older)
	}
	request(base, "invalid", 400)
	request(base, strings.Repeat(".", 129), 400)
	request(base, "0.0.x.x", 400)
	request(base, "0.0.7fffffff.x", 409)
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	request(base, page.Before, 409)
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 110)), 0600); err != nil {
		t.Fatal(err)
	}
	request(base, page.Before, 409)
	request(mountServer(t, dir, ""), "", 503)
	request(mountServer(t, dir, filepath.Join(dir, "absent")), "", 404)
	request(mountServer(t, dir, dir), "", 500)
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	request(mountServer(t, dir, filepath.Join(dir, "logs")), "", 500)
}

func TestHistoryReportsUnreadableFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = filePage(f, "", 100); err == nil {
		t.Fatal("closed file unexpectedly readable")
	}
}

func TestHistoryTracksFileAcrossAppends(t *testing.T) {
	p := filepath.Join(t.TempDir(), "game.log")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	latest, err := filePage(f, "", 2)
	if err != nil || latest.Text != "three\nfour\n" || latest.Before == "" {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	if _, err = f.WriteString("five\n"); err != nil {
		t.Fatal(err)
	}
	older, err := filePage(f, latest.Before, 2)
	if err != nil || older.Text != "one\ntwo\n" || older.Before != "" {
		t.Fatalf("older=%+v err=%v", older, err)
	}
	replacement, err := os.CreateTemp(t.TempDir(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if _, err = filePage(replacement, latest.Before, 2); !errors.Is(err, errHistoryChanged) {
		t.Fatalf("replacement: %v", err)
	}
	if err = f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("new\nnew\nnew\n"); err != nil {
		t.Fatal(err)
	}
	if _, err = filePage(f, latest.Before, 2); !errors.Is(err, errHistoryChanged) {
		t.Fatalf("truncate and regrow: %v", err)
	}
}

func TestHistoryBoundsLongFileLines(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := strings.Repeat("ä", historyBytes) + "\n"
	if _, err = f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	var before, joined string
	for i := 0; i < 5; i++ {
		page, err := filePage(f, before, 2)
		if err != nil || len(page.Text) > historyBytes {
			t.Fatalf("bytes=%d err=%v", len(page.Text), err)
		}
		joined = page.Text + joined
		before = page.Before
		if before == "" {
			break
		}
	}
	if joined != line {
		t.Fatal("long line changed across page boundaries")
	}
}

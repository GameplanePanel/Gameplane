package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	if _, err = f.WriteString("one\ntwo\n"); err != nil {
		t.Fatal(err)
	}
	page, err := filePage(f, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	writeOnly, err := os.OpenFile(f.Name(), os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writeOnly.Close()
	if _, err = filePage(writeOnly, "", 1); err == nil {
		t.Fatal("write-only file was readable")
	}
	if _, err = filePage(writeOnly, page.Before, 1); !errors.Is(err, errHistoryChanged) {
		t.Fatalf("lost cursor: %v", err)
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

func TestHistoryRejectsRegrowthWithSameAdjacentLines(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tail := strings.Repeat("same status line\n", 16)
	if _, err := f.WriteString("first-run\n" + tail); err != nil {
		t.Fatal(err)
	}
	page, err := filePage(f, "", 2)
	if err != nil || page.Before == "" {
		t.Fatalf("first: %+v %v", page, err)
	}
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("other-run\n"+tail), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := filePage(f, page.Before, 2); !errors.Is(err, errHistoryChanged) {
		t.Fatalf("accepted a different preceding page after copytruncate: %v", err)
	}
}

func TestLongLiveRecordLeavesTheNextRecordReadable(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(strings.Repeat("x", 2*historyBytes) + "\nnext\n"))
	line, err := readLogLine(t.Context(), reader)
	if err != nil || len(line) > 32<<10 || !strings.Contains(line, "truncated") {
		t.Fatalf("long record: %d bytes, %v", len(line), err)
	}
	line, err = readLogLine(t.Context(), reader)
	if err != nil || line != "next\n" {
		t.Fatalf("next record: %q %v", line, err)
	}
}

func TestReplaySkipsHugePartialRecordAndContinuesLive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "game.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(maxTailBytes + 2048); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\nrecent\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(mountServer(t, dir, path), "http") + "/logs/tail?tail=10"
	conn, resp, err := websocket.Dial(ctx, endpoint, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if _, msg, err := conn.Read(ctx); err != nil || string(msg) != "recent\n" {
		t.Fatalf("replay at bounded snapshot: %d bytes, %v", len(msg), err)
	}
	if _, err := file.WriteString(strings.Repeat("z", historyBytes) + "\nnext\n"); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := conn.Read(ctx); err != nil || !strings.Contains(string(msg), "[log line truncated]") {
		t.Fatalf("oversized live frame: %d bytes, %v", len(msg), err)
	}
	if _, msg, err := conn.Read(ctx); err != nil || string(msg) != "next\n" {
		t.Fatalf("next frame: %q, %v", msg, err)
	}
}

// A rewrite between the main read and either cursor read must never mix two
// generations in one successful response.
type changingHistoryFile struct {
	*os.File
	onRead func(int)
	reads  int
}

func (f *changingHistoryFile) ReadAt(p []byte, off int64) (int, error) {
	f.reads++
	f.onRead(f.reads)
	return f.File.ReadAt(p, off)
}

func TestHistoryRejectsRewriteDuringRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		at     int
		regrow bool
	}{
		{"before-page", 1, false}, {"before-next-cursor", 2, false},
		{"before-recheck", 3, false}, {"regrown-before-recheck", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "log")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("one\ntwo\nthree\n"); err != nil {
				t.Fatal(err)
			}
			changed := &changingHistoryFile{File: f, onRead: func(n int) {
				if n != tc.at {
					return
				}
				if err := f.Truncate(0); err != nil {
					t.Fatal(err)
				}
				if tc.regrow {
					if _, err := f.WriteAt([]byte("new\nnew\nother\n"), 0); err != nil {
						t.Fatal(err)
					}
				}
			}}
			if _, err := filePage(changed, "", 1); !errors.Is(err, errHistoryChanged) {
				t.Fatalf("mixed source accepted: %v", err)
			}
		})
	}
}

func TestCanceledLiveRecordRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readLogLine(ctx, bufio.NewReader(strings.NewReader("hello"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
}

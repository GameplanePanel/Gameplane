package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestPodHistoryAnchorAcrossAppend(t *testing.T) {
	rows := []string{"2026-01-01T12:00:00.000000001Z one", "2026-01-01T12:00:00.000000002Z two", "2026-01-01T12:00:00.000000003Z two"}
	anchor := podRecordID(rows[2])
	rows = append(rows, "2026-01-01T12:00:01Z new")
	end, err := podBoundary(rows, anchor)
	older, start, _ := podPageRows(rows, end, 2)
	if err != nil || start != 0 || strings.Join(older, ",") != "one,two" {
		t.Fatalf("older=%v err=%v", older, err)
	}
	if _, err = podBoundary(rows[3:], anchor); !errors.Is(err, errPodHistoryChanged) {
		t.Fatalf("lost anchor: %v", err)
	}
	if _, err = podBoundary(append(rows, rows[2]), anchor); !errors.Is(err, errPodHistoryChanged) {
		t.Fatalf("ambiguous anchor: %v", err)
	}
}

func TestPodHistoryRoutesAndContinuity(t *testing.T) {
	pod := newPod("alpha-0", []string{"setup"}, false)
	pod.Status.ContainerStatuses[0].ContainerID = "containerd://game"
	fakeClient := fake.NewSimpleClientset(pod)
	registry := streamTestRegistry(&kube.Client{Typed: fakeClient})
	rows := []string{}
	appendRows := func(n int) {
		for i := 0; i < n; i++ {
			stamp := time.Unix(1710000000+int64(len(rows)), 0).UTC()
			rows = append(rows, fmt.Sprintf("%s row-%d", stamp.Format(time.RFC3339Nano), len(rows)))
		}
	}
	appendRows(150)
	var readSince, replaceDuringRead bool
	fakeClient.PrependReactor("get", "pods/log", func(action ktesting.Action) (bool, runtime.Object, error) {
		opts := action.(ktesting.GenericAction).GetValue().(*corev1.PodLogOptions)
		if opts.LimitBytes == nil || *opts.LimitBytes > podHistoryReadBytes+1 || !opts.Timestamps {
			t.Fatal("unbounded pod read")
		}
		if replaceDuringRead {
			replacement := pod.DeepCopy()
			replacement.UID = "replacement"
			if err := fakeClient.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), replacement, pod.Namespace); err != nil {
				t.Fatal(err)
			}
		}
		selected := rows
		if opts.Container == "setup" {
			selected = []string{"2026-01-01T12:00:00Z setup-output"}
		}
		if opts.TailLines != nil {
			selected = selected[max(0, len(selected)-int(*opts.TailLines)):]
		}
		if opts.SinceTime != nil {
			readSince = true
			for len(selected) > 0 {
				ts, _, _ := strings.Cut(selected[0], " ")
				stamp, _ := time.Parse(time.RFC3339Nano, ts)
				if !stamp.Before(opts.SinceTime.Time) {
					break
				}
				selected = selected[1:]
			}
		}
		return true, &runtime.Unknown{Raw: []byte(strings.Join(selected, "\n") + "\n")}, nil
	})
	router := chi.NewRouter()
	mountPodLogs(router, registry)
	read := func(before string, want int) podHistoryPage {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/logs/pod/history?before="+url.QueryEscape(before), nil)
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
		}
		var page podHistoryPage
		if want == 200 {
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		return page
	}
	read("%", 400)
	read(strings.Repeat("x", 2049), 400)
	first := read("", 200)
	if first.Source != "game" || !strings.HasPrefix(first.Text, "row-50\n") || !strings.HasSuffix(first.Text, "row-149\n") {
		t.Fatalf("first=%+v", first)
	}
	appendRows(600)
	older := read(first.Before, 200)
	if !readSince || !strings.HasPrefix(older.Text, "row-0\n") || !strings.HasSuffix(older.Text, "row-49\n") {
		t.Fatalf("older=%+v", older)
	}
	setup := read(older.Before, 200)
	if setup.Text != "setup-output\n" || setup.Source != "setup" || setup.Before != "" {
		t.Fatalf("setup=%+v", setup)
	}
	// One MiB from the beginning of a 164-line tail misses the newest
	// records. A smaller bounded tail can still return useful pages.
	for i := range rows {
		rows[i] += " " + strings.Repeat("a", 8000)
	}
	large := read("", 200)
	if !strings.Contains(large.Text, "row-749 ") || len(large.Text) > podHistoryBytes || large.Before == "" {
		t.Fatalf("missing latest records from the capped window: %d bytes", len(large.Text))
	}
	previous := read(large.Before, 200)
	if previous.Text == "" || strings.Contains(previous.Text, "row-749 ") {
		t.Fatalf("missing preceding records from the capped window: %d bytes", len(previous.Text))
	}
	pod.Status.ContainerStatuses[0].RestartCount++
	if _, err := fakeClient.CoreV1().Pods(pod.Namespace).UpdateStatus(t.Context(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	read(first.Before, 409)
	rows = []string{"2026-01-01T12:00:00Z " + strings.Repeat("x", podHistoryReadBytes+1), "2026-01-01T12:00:01Z latest"}
	afterGiant := read("", 200)
	if afterGiant.Text != "latest\n" || !afterGiant.Limited {
		t.Fatalf("newest output after a large record: %+v", afterGiant)
	}
	rows = rows[:1]
	if page := read("", 200); !page.Limited || page.Text != "" {
		t.Fatalf("oversized record: %+v", page)
	}
	replaceDuringRead = true
	read("", 409)
}

func TestPodHistoryCapsLargeRecords(t *testing.T) {
	rows := []string{"2026-01-01T12:00:00Z " + strings.Repeat("a", podHistoryBytes), "2026-01-01T12:00:01Z next"}
	lines, start, limited := podPageRows(rows, 2, 100)
	if limited || len(lines) != 1 || start != 1 {
		t.Fatal("page crossed its byte bound")
	}
	lines, start, limited = podPageRows(rows, 1, 100)
	if !limited || len(lines[0]) > podHistoryBytes || start != 0 {
		t.Fatal("long line was not clipped")
	}
}

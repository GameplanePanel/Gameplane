package httperr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func newReq() *http.Request {
	return httptest.NewRequestWithContext(context.Background(), "GET", "/x", nil)
}

// TestWriteCode_5xxNeverEchoesErr is the B1 defence-in-depth regression
// test: a >=500 WriteCode call must never put the supplied err's text in
// the response body — several callers pass an unsanitized upstream error
// (e.g. a mod-registry GET failure that could carry an API key in a
// *url.Error's URL) straight through. The real error is still logged in
// full server-side (not asserted here); the HTTP response only gets the
// generic status text.
func TestWriteCode_5xxNeverEchoesErr(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteCode(rr, newReq(), http.StatusInternalServerError, errors.New("boom"))
	if rr.Code != 500 {
		t.Fatalf("code=%d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "boom") {
		t.Fatalf("body leaks the raw error: %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Internal Server Error") {
		t.Fatalf("body = %q, want the generic status text", rr.Body.String())
	}
}

// TestWriteCode_502NeverEchoesErr mirrors the real B1 shape: registry.go's
// handlers wrap upstream mod-registry errors as 502s.
func TestWriteCode_502NeverEchoesErr(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteCode(rr, newReq(), http.StatusBadGateway,
		errors.New(`registry GET: Get "https://api.steampowered.com/x?key=s3cret-steam-key": dial tcp: connection refused`))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("code=%d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "s3cret-steam-key") {
		t.Fatalf("body leaks the API key: %q", rr.Body.String())
	}
}

func TestWriteCode_4xx(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteCode(rr, newReq(), http.StatusBadRequest, errors.New("bad"))
	if rr.Code != 400 {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestWrite_Classification(t *testing.T) {
	gr := schema.GroupResource{Resource: "x"}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"forbidden namespace", scope.ErrForbiddenNamespace, http.StatusForbidden},
		{"forbidden cluster", scope.ErrForbiddenCluster, http.StatusBadRequest},
		{"not found", apierrors.NewNotFound(gr, "n"), http.StatusNotFound},
		{"already exists", apierrors.NewAlreadyExists(gr, "n"), http.StatusConflict},
		{"resourceVersion conflict", apierrors.NewConflict(gr, "n", errors.New("modified")), http.StatusConflict},
		{"forbidden", apierrors.NewForbidden(gr, "n", errors.New("rbac")), http.StatusForbidden},
		{"unauthorized", apierrors.NewUnauthorized("nope"), http.StatusUnauthorized},
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Group: "gameplane.local", Kind: "X"}, "n", nil), http.StatusUnprocessableEntity},
		{"bad request", apierrors.NewBadRequest("nope"), http.StatusBadRequest},
		{"empty body (io.EOF)", io.EOF, http.StatusBadRequest},
		{"share link not found (F-082)", fmt.Errorf("revoke share link %q: %w", "x", db.ErrShareLinkNotFound), http.StatusNotFound},
		{"share link not found, wrapped again", fmt.Errorf("handler: %w", db.ErrShareLinkNotFound), http.StatusNotFound},
		{"unknown", errors.New("mystery"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Write(rr, newReq(), tc.err)
			if rr.Code != tc.want {
				t.Fatalf("got %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

func TestWrite_InvalidEchoesAPIErrMessage(t *testing.T) {
	se := &apierrors.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Reason:  metav1.StatusReasonInvalid,
			Message: "name must be lowercase",
			Code:    http.StatusUnprocessableEntity,
		},
	}
	rr := httptest.NewRecorder()
	Write(rr, newReq(), se)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "name must be lowercase") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

func TestApiErrMessage_FallbackForNonStatusError(t *testing.T) {
	if got := apiErrMessage(errors.New("plain")); got != "invalid" {
		t.Fatalf("got %q", got)
	}
}

// TestWrite_ConflictBodyDistinctFromAlreadyExists pins the two 409s apart.
// Handlers hand-build their own meaningful 409s via WriteCode ("capture
// already in progress on this server", "role is assigned to one or more
// users", …), and classify() now maps two distinct K8s errors onto the same
// status. The status alone was never the contract — the body is what
// callers and the e2e suite match on — so the messages must not converge.
func TestWrite_ConflictBodyDistinctFromAlreadyExists(t *testing.T) {
	gr := schema.GroupResource{Resource: "networkcaptures"}

	rr := httptest.NewRecorder()
	Write(rr, newReq(), apierrors.NewConflict(gr, "cap-1", errors.New("the object has been modified")))
	conflictBody := rr.Body.String()
	if rr.Code != http.StatusConflict {
		t.Fatalf("conflict code = %d, want 409", rr.Code)
	}
	if !strings.Contains(conflictBody, "conflict, retry") {
		t.Fatalf("conflict body = %q, want the retryable-conflict message", conflictBody)
	}
	// The raw error names the resource and the apiserver's internals; only
	// the generic message may reach the caller.
	if strings.Contains(conflictBody, "networkcaptures") {
		t.Fatalf("conflict body leaks the resource: %q", conflictBody)
	}

	rr2 := httptest.NewRecorder()
	Write(rr2, newReq(), apierrors.NewAlreadyExists(gr, "cap-1"))
	if got := rr2.Body.String(); got == conflictBody {
		t.Fatalf("already-exists and conflict share the body %q", got)
	}
}

// problemCase is one classify branch with the status, English text and
// message id the response must carry.
type problemCase struct {
	name   string
	err    error
	status int
	msg    string
	id     string
}

func problemCases() []problemCase {
	gr := schema.GroupResource{Resource: "x"}
	invalid := &apierrors.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Reason:  metav1.StatusReasonInvalid,
			Message: "name must be lowercase",
			Code:    http.StatusUnprocessableEntity,
		},
	}
	return []problemCase{
		{"forbidden namespace", scope.ErrForbiddenNamespace, http.StatusForbidden, "namespace not permitted", IDNamespaceForbidden},
		{"forbidden cluster", scope.ErrForbiddenCluster, http.StatusBadRequest, "cluster not permitted", IDClusterForbidden},
		{"not found", apierrors.NewNotFound(gr, "n"), http.StatusNotFound, "not found", IDNotFound},
		{"share link not found", fmt.Errorf("x: %w", db.ErrShareLinkNotFound), http.StatusNotFound, "not found", IDNotFound},
		{"already exists", apierrors.NewAlreadyExists(gr, "n"), http.StatusConflict, "already exists", IDAlreadyExists},
		{"resourceVersion conflict", apierrors.NewConflict(gr, "n", errors.New("modified")), http.StatusConflict, "conflict, retry", IDConflictRetry},
		{"forbidden", apierrors.NewForbidden(gr, "n", errors.New("rbac")), http.StatusForbidden, "forbidden", IDForbidden},
		{"unauthorized", apierrors.NewUnauthorized("nope"), http.StatusUnauthorized, "unauthorized", IDUnauthorized},
		{"invalid", invalid, http.StatusUnprocessableEntity, "name must be lowercase", IDInvalid},
		{"bad request", apierrors.NewBadRequest("nope"), http.StatusBadRequest, "bad request", IDBadRequest},
		{"empty body (io.EOF)", io.EOF, http.StatusBadRequest, "empty body", IDEmptyBody},
		{"unknown", errors.New("mystery"), http.StatusInternalServerError, "internal error", IDInternal},
		{"registry message", fmt.Errorf("wrap: %w", &Message{ID: "x.teapot", Status: http.StatusTeapot, English: "short and stout"}), http.StatusTeapot, "short and stout", "x.teapot"},
	}
}

// acceptReq is newReq with an Accept header set.
func acceptReq(accept string) *http.Request {
	r := newReq()
	r.Header.Set("Accept", accept)
	return r
}

// TestWrite_PlainBodyByteIdentical pins that every response that does not ask
// for a problem document is exactly what http.Error wrote before this change.
func TestWrite_PlainBodyByteIdentical(t *testing.T) {
	accepts := []string{"", "*/*", "application/problem+json;q=0"}
	for _, tc := range problemCases() {
		for _, accept := range accepts {
			t.Run(fmt.Sprintf("%s/accept=%q", tc.name, accept), func(t *testing.T) {
				rr := httptest.NewRecorder()
				Write(rr, acceptReq(accept), tc.err)

				want := httptest.NewRecorder()
				http.Error(want, tc.msg, tc.status)

				if rr.Code != want.Code {
					t.Fatalf("code = %d, want %d", rr.Code, want.Code)
				}
				if rr.Body.String() != want.Body.String() {
					t.Fatalf("body = %q, want %q", rr.Body.String(), want.Body.String())
				}
				if got, want := rr.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
					t.Fatalf("Content-Type = %q, want %q", got, want)
				}
				if got, want := rr.Header().Get("X-Content-Type-Options"), "nosniff"; got != want {
					t.Fatalf("X-Content-Type-Options = %q, want %q", got, want)
				}
			})
		}
	}
}

// decodedProblem mirrors the problem document fields the tests check.
type decodedProblem struct {
	Type      string         `json:"type"`
	Status    int            `json:"status"`
	Title     string         `json:"title"`
	Detail    string         `json:"detail"`
	MessageID string         `json:"messageId"`
	Args      map[string]any `json:"args"`
}

// TestWrite_ProblemDocument checks the problem response for every classify
// branch when the caller accepts application/problem+json.
func TestWrite_ProblemDocument(t *testing.T) {
	for _, tc := range problemCases() {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Write(rr, acceptReq("application/json, application/problem+json"), tc.err)

			if rr.Code != tc.status {
				t.Fatalf("code = %d, want %d", rr.Code, tc.status)
			}
			if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("Content-Type = %q", got)
			}
			if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
			body := rr.Body.String()
			if strings.Count(body, "\n") != 1 || !strings.HasSuffix(body, "\n") {
				t.Fatalf("body must be one JSON line plus newline: %q", body)
			}
			var doc decodedProblem
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatalf("decode %q: %v", body, err)
			}
			if doc.Type != "about:blank" {
				t.Fatalf("type = %q", doc.Type)
			}
			if doc.Status != tc.status {
				t.Fatalf("status = %d, want %d", doc.Status, tc.status)
			}
			if doc.Title != http.StatusText(tc.status) {
				t.Fatalf("title = %q, want %q", doc.Title, http.StatusText(tc.status))
			}
			if doc.Detail != tc.msg {
				t.Fatalf("detail = %q, want %q", doc.Detail, tc.msg)
			}
			if doc.MessageID != tc.id {
				t.Fatalf("messageId = %q, want %q", doc.MessageID, tc.id)
			}
			if doc.Args == nil {
				t.Fatalf("args must be an object, got null in %q", body)
			}
			if tc.id == IDInvalid && doc.Args["detail"] != tc.msg {
				t.Fatalf("args = %v, want detail %q", doc.Args, tc.msg)
			}
		})
	}
}

// TestWriteCode_ProblemDocument4xxUsesErrText checks a hand-built 4xx on a
// problem-document caller: the text is the error's, the id is the generic one.
func TestWriteCode_ProblemDocument4xxUsesErrText(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteCode(rr, acceptReq("application/problem+json"), http.StatusBadRequest, errors.New("ref is required"))
	var doc decodedProblem
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if doc.MessageID != "server.generic.400" {
		t.Fatalf("messageId = %q, want server.generic.400", doc.MessageID)
	}
	if doc.Detail != "ref is required" {
		t.Fatalf("detail = %q", doc.Detail)
	}
}

// TestWriteCode_ProblemDocument5xxNeverLeaks checks a >=500 WriteCode on a
// problem-document caller: the detail is the status text, never the error.
func TestWriteCode_ProblemDocument5xxNeverLeaks(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteCode(rr, acceptReq("application/problem+json"), http.StatusInternalServerError,
		errors.New("boom with s3cret-token"))
	if strings.Contains(rr.Body.String(), "s3cret-token") {
		t.Fatalf("body leaks the raw error: %q", rr.Body.String())
	}
	var doc decodedProblem
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if doc.Detail != "Internal Server Error" {
		t.Fatalf("detail = %q, want the status text", doc.Detail)
	}
	if doc.MessageID != "server.generic.500" {
		t.Fatalf("messageId = %q, want server.generic.500", doc.MessageID)
	}
}

// TestWriteCode_MessageKeepsIDAndArgs checks that a *Message in a 4xx
// WriteCode keeps its id and args for a problem-document caller.
func TestWriteCode_MessageKeepsIDAndArgs(t *testing.T) {
	m := &Message{ID: "x.custom", Status: http.StatusConflict, English: "custom text", Args: map[string]any{"n": 3}}
	rr := httptest.NewRecorder()
	WriteCode(rr, acceptReq("application/problem+json"), http.StatusConflict, m)
	var doc decodedProblem
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	if doc.MessageID != "x.custom" || doc.Detail != "custom text" || doc.Args["n"] != float64(3) {
		t.Fatalf("doc = %+v", doc)
	}
}

// TestWriteMessage_Custom covers the custom-message path: the status, the
// plain body, the problem document, and the >=500 masking rule.
func TestWriteMessage_Custom(t *testing.T) {
	m := &Message{ID: "x.custom", Status: http.StatusConflict, English: "custom text", Args: map[string]any{"n": 3}}

	rr := httptest.NewRecorder()
	WriteMessage(rr, acceptReq(""), m)
	if rr.Code != http.StatusConflict || rr.Body.String() != "custom text\n" {
		t.Fatalf("plain: code=%d body=%q", rr.Code, rr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	WriteMessage(rr2, acceptReq("application/problem+json"), m)
	var doc decodedProblem
	if err := json.Unmarshal(rr2.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %q: %v", rr2.Body.String(), err)
	}
	if rr2.Code != http.StatusConflict || doc.MessageID != "x.custom" || doc.Detail != "custom text" || doc.Args["n"] != float64(3) {
		t.Fatalf("problem: code=%d doc=%+v", rr2.Code, doc)
	}
}

func TestWriteMessage_ServerErrorMasked(t *testing.T) {
	m := &Message{ID: "x.boom", Status: http.StatusServiceUnavailable, English: "secret upstream text"}
	rr := httptest.NewRecorder()
	WriteMessage(rr, acceptReq(""), m)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rr.Code)
	}
	if rr.Body.String() != "Service Unavailable\n" {
		t.Fatalf("body = %q, want the status text", rr.Body.String())
	}
}

func TestWriteMessage_RemoteClusterKeepsText(t *testing.T) {
	m := &Message{ID: IDRemoteClusterNotImplemented, Status: http.StatusNotImplemented, English: RemoteClusterNotImplemented}
	rr := httptest.NewRecorder()
	WriteMessage(rr, acceptReq(""), m)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("code = %d", rr.Code)
	}
	if rr.Body.String() != RemoteClusterNotImplemented+"\n" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}

func TestWriteMessage_NilAndZeroStatusAre500(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteMessage(rr, acceptReq(""), nil)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("nil message: code = %d", rr.Code)
	}
	rr2 := httptest.NewRecorder()
	WriteMessage(rr2, acceptReq(""), &Message{ID: "x.zero", English: "zero status"})
	if rr2.Code != http.StatusInternalServerError {
		t.Fatalf("zero status: code = %d", rr2.Code)
	}
}

// TestWrite_WrappedMessage checks errors.As through Write with a wrapped
// *Message: the status, the plain text and the problem id all come from it.
func TestWrite_WrappedMessage(t *testing.T) {
	m := &Message{ID: "x.field", Status: http.StatusUnprocessableEntity, English: "bad field", Args: map[string]any{"detail": "bad field"}}
	err := fmt.Errorf("x: %w", m)

	rr := httptest.NewRecorder()
	Write(rr, acceptReq(""), err)
	if rr.Code != http.StatusUnprocessableEntity || rr.Body.String() != "bad field\n" {
		t.Fatalf("plain: code=%d body=%q", rr.Code, rr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	Write(rr2, acceptReq("application/problem+json"), err)
	var doc decodedProblem
	if err := json.Unmarshal(rr2.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode %q: %v", rr2.Body.String(), err)
	}
	if doc.MessageID != "x.field" || doc.Args["detail"] != "bad field" {
		t.Fatalf("doc = %+v", doc)
	}
}

// TestWantsProblem covers the Accept header negotiation.
func TestWantsProblem(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		want   bool
	}{
		{"empty", "", false},
		{"any type", "*/*", false},
		{"application wildcard", "application/*", false},
		{"json only", "application/json", false},
		{"problem json", "application/problem+json", true},
		{"mixed case", "Application/Problem+JSON", true},
		{"q zero", "application/problem+json;q=0", false},
		{"q half", "application/problem+json; q=0.5", true},
		{"listed after text", "text/html, application/problem+json;q=0.1", true},
		{"bad q", "application/problem+json;q=bogus", false},
		{"json q zero then problem", "application/json;q=0, application/problem+json", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WantsProblem(acceptReq(tc.accept)); got != tc.want {
				t.Fatalf("WantsProblem(%q) = %v, want %v", tc.accept, got, tc.want)
			}
		})
	}
	if WantsProblem(nil) {
		t.Fatal("WantsProblem(nil) = true")
	}
}

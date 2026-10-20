package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// telKubeExt returns a fake Kubernetes client with all list kinds registered,
// needed for tests that use extended telemetry.
func telKubeExt(objs ...runtime.Object) *kube.Client {
	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRs["servers"]:   "GameServerList",
		kube.GVRs["templates"]: "GameTemplateList",
		kube.GVRs["schedules"]: "BackupScheduleList",
		kube.GVRCluster:        "ClusterList",
		kube.GVRModuleSource:   "ModuleSourceList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvkr, objs...)
	return &kube.Client{Dynamic: dyn}
}

// Spec 022 US7 (T105, T106): before each POST the reporter asks the provider
// for a proof-of-work challenge, solves it and sends the solution; a provider
// that offers none gets reports without the header (research R21, FR-040).

// powPosted is one POST the proof-of-work provider received.
type powPosted struct {
	path      string
	header    string // the proof-of-work header, "" when absent
	signature string
	body      []byte
	// token is the challenge the header named, and solved is whether the
	// header carried a valid, unused solution of an issued challenge.
	token  string
	solved bool
}

// powProvider is a test telemetry provider that offers challenges.
type powProvider struct {
	mu sync.Mutex
	// bits is the difficulty of every challenge it issues.
	bits int
	// getStatus, when not 0, is the status of GET /v1/challenge with an empty
	// body (404 means the provider offers no challenges). getBody, when not
	// empty, is sent as the body of a 200 instead of a real challenge.
	getStatus int
	getBody   string
	// expiresIn is how long challenges live, counted from clock; expiresRaw,
	// when not empty, replaces the expiresAt value.
	clock      *fakeClock
	expiresIn  time.Duration
	expiresRaw string
	// status picks the status of the n-th POST (1-based); nil means 204.
	status func(n int, p powPosted) int

	issued   map[string]bool // token to used
	getPaths []string
	getAuth  []string
	posts    []powPosted
}

// newPoWProvider starts a provider with the given difficulty on a fake clock.
func newPoWProvider(t *testing.T, clk *fakeClock, bits int) (*httptest.Server, *powProvider) {
	t.Helper()
	p := &powProvider{bits: bits, clock: clk, expiresIn: 15 * time.Minute, issued: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(srv.Close)
	return srv, p
}

func (p *powProvider) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Method == http.MethodGet {
		p.getPaths = append(p.getPaths, r.URL.Path)
		p.getAuth = append(p.getAuth, r.Header.Get("Authorization"))
		switch {
		case p.getStatus != 0:
			w.WriteHeader(p.getStatus)
		case p.getBody != "":
			_, _ = io.WriteString(w, p.getBody)
		default:
			token := "challenge-" + strconv.Itoa(len(p.issued)+1)
			p.issued[token] = false
			expires := p.clock.Now().Add(p.expiresIn).UTC().Format(time.RFC3339)
			if p.expiresRaw != "" {
				expires = p.expiresRaw
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"challenge": token, "bits": p.bits, "expiresAt": expires})
		}
		return
	}
	body, _ := io.ReadAll(r.Body)
	rec := powPosted{
		path: r.URL.Path, header: r.Header.Get(telemetryschema.PoWHeader),
		signature: r.Header.Get(telemetryschema.SignatureHeader), body: body,
	}
	if token, nonce, err := telemetryschema.ParsePoW(rec.header); err == nil {
		rec.token = token
		used, issued := p.issued[token]
		rec.solved = issued && !used && telemetryschema.PoWOK(token, nonce, p.bits)
		if issued {
			p.issued[token] = true
		}
	}
	p.posts = append(p.posts, rec)
	status := http.StatusNoContent
	if p.status != nil {
		status = p.status(len(p.posts), rec)
	}
	w.WriteHeader(status)
}

func (p *powProvider) postCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.posts)
}

func (p *powProvider) post(i int) powPosted {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posts[i]
}

func (p *powProvider) getCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.getPaths)
}

func TestChallengeURL(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://telemetry.example.com/ingest":            "https://telemetry.example.com/v1/challenge",
		"https://telemetry.example.com/prefix/ingest":     "https://telemetry.example.com/prefix/v1/challenge",
		"https://telemetry.example.com/a/b/c/ingest":      "https://telemetry.example.com/a/b/c/v1/challenge",
		"https://telemetry.example.com":                   "https://telemetry.example.com/v1/challenge",
		"https://telemetry.example.com/":                  "https://telemetry.example.com/v1/challenge",
		"https://telemetry.example.com/prefix/":           "https://telemetry.example.com/prefix/v1/challenge",
		"http://gameplane-telemetry-receiver:8080/ingest": "http://gameplane-telemetry-receiver:8080/v1/challenge",
		"https://telemetry.example.com/ingest?x=1#frag":   "https://telemetry.example.com/v1/challenge",
	} {
		got, err := challengeURL(endpoint)
		if err != nil || got != want {
			t.Errorf("challengeURL(%q) = %q, %v, want %q", endpoint, got, err, want)
		}
	}
	if _, err := challengeURL("http://[::1"); err == nil {
		t.Error("challengeURL of an unparsable endpoint must fail")
	}
}

func TestReporter_ProviderWithoutChallengesGetsReportsWithoutTheHeader(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 10)
	prov.getStatus = http.StatusNotFound
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)

	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if prov.postCount() != 1 || prov.post(0).header != "" {
		t.Fatalf("posts = %d header %q, want one POST with no proof-of-work header", prov.postCount(), prov.post(0).header)
	}
	if prov.getCount() != 1 {
		t.Fatalf("challenge requests = %d, want 1", prov.getCount())
	}
	if st := mustState(t, store); st.LastOutcome != outcomeOK || st.ConsecutiveFailures != 0 {
		t.Fatalf("state = %+v, want ok", st)
	}
}

func TestReporter_AnyNon200ChallengeAnswerMeansNoProofOfWork(t *testing.T) {
	for name, set := range map[string]func(*powProvider){
		"server error":   func(p *powProvider) { p.getStatus = http.StatusInternalServerError },
		"rate limited":   func(p *powProvider) { p.getStatus = http.StatusTooManyRequests },
		"not JSON":       func(p *powProvider) { p.getBody = "<html>hello</html>" },
		"no challenge":   func(p *powProvider) { p.getBody = `{"bits":3}` },
		"truncated JSON": func(p *powProvider) { p.getBody = `{"challenge":"x"` },
	} {
		t.Run(name, func(t *testing.T) {
			clk := newFakeClock()
			srv, prov := newPoWProvider(t, clk, 10)
			set(prov)
			r := testReporter(telStore(t, true), telKube(), srv.URL, clk)
			if err := sendNow(t, r); err != nil {
				t.Fatalf("attempt: %v", err)
			}
			if prov.postCount() != 1 || prov.post(0).header != "" {
				t.Fatalf("posts = %d header %q, want one POST with no header", prov.postCount(), prov.post(0).header)
			}
		})
	}
}

func TestReporter_UnreachableChallengeEndpointFallsBackToThePost(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 10)
	r := testReporter(telStore(t, true), telKube(), srv.URL, clk)
	r.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return nil, errors.New("connection refused")
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if prov.postCount() != 1 || prov.post(0).header != "" {
		t.Fatalf("posts = %d header %q, want one POST with no header", prov.postCount(), prov.post(0).header)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReporter_SolvesTheChallengeAndSendsAHeaderThatVerifies(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 10)
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL+"/prefix/ingest", clk)
	r.auth = "Bearer secret"

	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if prov.postCount() != 1 {
		t.Fatalf("posts = %d, want 1", prov.postCount())
	}
	p := prov.post(0)
	token, nonce, err := telemetryschema.ParsePoW(p.header)
	if err != nil {
		t.Fatalf("header %q does not parse: %v", p.header, err)
	}
	if !telemetryschema.PoWOK(token, nonce, 10) || !p.solved {
		t.Fatalf("header %q is not a valid solution of a 10-bit challenge", p.header)
	}
	if p.path != "/prefix/ingest" || prov.getPaths[0] != "/prefix/v1/challenge" {
		t.Fatalf("paths: POST %q, challenge GET %q, want /prefix/ingest and /prefix/v1/challenge", p.path, prov.getPaths[0])
	}
	if prov.getAuth[0] != "" {
		t.Fatalf("the challenge request carried Authorization %q, want none", prov.getAuth[0])
	}
	if st := mustState(t, store); st.LastOutcome != outcomeOK || st.ConsecutiveFailures != 0 {
		t.Fatalf("state = %+v, want ok", st)
	}
}

func TestReporter_ZeroBitChallengeStillSendsTheHeader(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 0)
	prov.expiresRaw = "not a time" // an unreadable expiry means the default lifetime
	r := testReporter(telStore(t, true), telKube(), srv.URL, clk)
	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if p := prov.post(0); !p.solved || p.header != p.token+":0" {
		t.Fatalf("header %q solved %v, want %q accepted", p.header, p.solved, p.token+":0")
	}
}

func TestReporter_428GetsOneNewChallengeAndOneResend(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 8)
	prov.status = func(n int, _ powPosted) int {
		if n == 1 {
			return http.StatusPreconditionRequired
		}
		return http.StatusNoContent
	}
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)

	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if prov.postCount() != 2 || prov.getCount() != 2 {
		t.Fatalf("posts = %d, challenge requests = %d, want 2 and 2", prov.postCount(), prov.getCount())
	}
	first, second := prov.post(0), prov.post(1)
	if first.token == second.token || !first.solved || !second.solved {
		t.Fatalf("tokens %q then %q (solved %v, %v), want two different, valid solutions", first.token, second.token, first.solved, second.solved)
	}
	if string(first.body) != string(second.body) {
		t.Fatal("the resend must carry the same report")
	}
	if st := mustState(t, store); st.LastOutcome != outcomeOK || st.ConsecutiveFailures != 0 {
		t.Fatalf("state = %+v, want ok", st)
	}
}

func TestReporter_SecondProofOfWorkRefusalIsFailed(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 8)
	prov.status = func(int, powPosted) int { return http.StatusPreconditionRequired }
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)

	if err := sendNow(t, r); err == nil {
		t.Fatal("two 428s in a row must fail the attempt")
	}
	if prov.postCount() != 2 || prov.getCount() != 2 {
		t.Fatalf("posts = %d, challenge requests = %d, want exactly 2 and 2", prov.postCount(), prov.getCount())
	}
	if st := mustState(t, store); st.LastOutcome != outcomeFailed || st.ConsecutiveFailures != 1 {
		t.Fatalf("state = %+v, want failed with normal backoff", st)
	}
}

func TestReporter_ChallengeAboveTheCapIsFailedWithoutAPost(t *testing.T) {
	for _, bits := range []int{telemetryschema.MaxPoWBits + 1, 99, -1} {
		t.Run(strconv.Itoa(bits), func(t *testing.T) {
			clk := newFakeClock()
			srv, prov := newPoWProvider(t, clk, bits)
			store := telStore(t, true)
			r := testReporter(store, telKube(), srv.URL, clk)

			err := sendNow(t, r)
			if !errors.Is(err, errPoWDifficulty) {
				t.Fatalf("attempt error = %v, want errPoWDifficulty", err)
			}
			if prov.postCount() != 0 {
				t.Fatalf("posts = %d, want none: the install must not solve or send", prov.postCount())
			}
			if st := mustState(t, store); st.LastOutcome != outcomeFailed || st.ConsecutiveFailures != 1 {
				t.Fatalf("state = %+v, want failed with normal backoff", st)
			}
		})
	}
}

func TestReporter_ChallengeThatExpiresTooSoonIsFailedWithoutAPost(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 8)
	prov.expiresIn = solveMargin // no time left once the margin is kept
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)

	if err := sendNow(t, r); err == nil {
		t.Fatal("a challenge with no solving time left must fail the attempt")
	}
	if prov.postCount() != 0 {
		t.Fatalf("posts = %d, want none", prov.postCount())
	}
	if st := mustState(t, store); st.LastOutcome != outcomeFailed {
		t.Fatalf("state = %+v, want failed", st)
	}
}

func TestReporter_SolvingStopsWhenTheContextIsDone(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 8)
	r := testReporter(telStore(t, true), telKube(), srv.URL, clk)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.solveChallenge(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("solveChallenge on a cancelled context = %v, want context.Canceled", err)
	}
	if prov.postCount() != 0 {
		t.Fatalf("posts = %d, want none", prov.postCount())
	}
}

func TestReporter_409RotationResendCarriesAFreshChallenge(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 8)
	prov.status = func(n int, _ powPosted) int {
		if n == 1 {
			return http.StatusConflict
		}
		return http.StatusNoContent
	}
	store := telStoreExt(t, true, true)
	r := testReporter(store, telKubeExt(), srv.URL, clk)

	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if prov.postCount() != 2 || prov.getCount() != 2 {
		t.Fatalf("posts = %d, challenge requests = %d, want the 409 and one re-POST, each with its own challenge", prov.postCount(), prov.getCount())
	}
	first, second := prov.post(0), prov.post(1)
	if first.token == "" || second.token == "" || first.token == second.token {
		t.Fatalf("tokens %q then %q, want two different challenges (they are single use)", first.token, second.token)
	}
	if !first.solved || !second.solved {
		t.Fatalf("solved = %v, %v, want both valid", first.solved, second.solved)
	}
	if first.signature == "" || second.signature == "" || first.signature == second.signature {
		t.Fatal("both POSTs must be signed, with different signatures after the rotation")
	}
	if st := mustState(t, store); st.LastOutcome != outcomeOK {
		t.Fatalf("state = %+v, want ok", st)
	}
}

func TestReporter_400FallbackResendCarriesAFreshChallenge(t *testing.T) {
	clk := newFakeClock()
	srv, prov := newPoWProvider(t, clk, 8)
	prov.status = func(n int, _ powPosted) int {
		if n == 1 {
			return http.StatusBadRequest
		}
		return http.StatusNoContent
	}
	r := testReporter(telStoreExt(t, true, true), telKubeExt(), srv.URL, clk)

	if err := sendNow(t, r); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if prov.postCount() != 2 || prov.getCount() != 2 {
		t.Fatalf("posts = %d, challenge requests = %d, want 2 and 2", prov.postCount(), prov.getCount())
	}
	first, second := prov.post(0), prov.post(1)
	if first.token == second.token || !second.solved {
		t.Fatalf("tokens %q then %q solved %v, want a fresh, valid challenge for the basic re-send", first.token, second.token, second.solved)
	}
	if second.signature != "" || strings.Contains(string(second.body), `"ext"`) {
		t.Fatal("the fallback re-send must be basic only")
	}
}

//go:build envtest

package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// envtest fires up a real kube-apiserver+etcd in-process. We then build
// the same kube.Client + chi router the production main wires, minus
// the auth/RBAC/audit middleware (those live in their own packages and
// have their own unit tests). Tests assert that handlers correctly
// shape requests at the apiserver.
//
// Per-test isolation: the suite uses a single shared namespace
// (scope.DefaultNamespace) because that namespace is hard-coded into
// the scope package at init time and can't be changed at runtime
// without touching the package. Tests therefore use unique resource
// names per test (uniqueResourceName) to avoid collisions.

var (
	testEnv           *envtest.Environment
	cfg               *rest.Config
	kubeC             *kube.Client
	apiSrv            *httptest.Server
	apiBase           string
	mountedR          *chi.Mux
	captureAuditStore *db.Store
)

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "operator", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic("envtest start: " + err.Error())
	}

	kubeC, err = kube.New(cfg)
	if err != nil {
		_ = testEnv.Stop()
		panic("kube client: " + err.Error())
	}

	if _, err := kubeC.Typed.CoreV1().Namespaces().Create(
		context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: scope.DefaultNamespace}},
		metav1.CreateOptions{},
	); err != nil {
		_ = testEnv.Stop()
		panic("create games namespace: " + err.Error())
	}

	// reg dispatches cluster-aware routes: kubeC as the default "local"
	// cluster (the real envtest apiserver), plus a second, empty "other"
	// cluster backed by a fake dynamic client so dispatch-isolation tests
	// can prove that a `?cluster=` selector never leaks objects across
	// clusters. MountResources, MountLifecycle, and MountDestinations take
	// reg; only MountModules still takes kubeC directly.
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, kubeC)
	fakeScheme := runtime.NewScheme()
	fakeDyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(fakeScheme, map[schema.GroupVersionResource]string{
		kube.GVRs["servers"]:   "GameServerList",
		kube.GVRs["templates"]: "GameTemplateList",
		kube.GVRs["backups"]:   "BackupList",
		kube.GVRs["schedules"]: "BackupScheduleList",
		kube.GVRs["restores"]:  "RestoreList",
	})
	reg.Set("other", &kube.Client{Dynamic: fakeDyn, Typed: k8sfake.NewSimpleClientset()})

	// Capture routes need a real Auditor (FR-006 writes are on the request
	// path itself, not just the generic middleware), so this suite opens
	// its own in-memory sqlite store for it. This DSN is deliberately NOT
	// the unnamed "file::memory:" newTestStore uses for the package's
	// non-envtest tests: SQLite's cache=shared attaches every unnamed
	// ":memory:" handle in the process to the SAME database, and that
	// database is kept alive by whichever connection closes last. Opened
	// here in TestMain (before m.Run) and closed after it, an unnamed DSN
	// would pin every other test's in-memory store alive and shared for
	// the whole package run — audit_events/config/users rows leaking
	// across every test in the package, plus lock contention from
	// multiple *Store handles (each capped at 1 open conn) on one
	// database. A distinct name gives this store its own database.
	captureAuditStore, err = db.Open(context.Background(), "sqlite", "file:captureaudit?mode=memory&cache=shared&_pragma=journal_mode(WAL)")
	if err != nil {
		_ = testEnv.Stop()
		panic("open capture audit store: " + err.Error())
	}
	if err := captureAuditStore.Migrate(context.Background()); err != nil {
		_ = captureAuditStore.Close()
		_ = testEnv.Stop()
		panic("migrate capture audit store: " + err.Error())
	}
	captureAuditor := audit.New(captureAuditStore)

	mountedR = chi.NewRouter()
	MountResources(mountedR, reg, nil)
	MountLifecycle(mountedR, reg)
	MountTunnelCredentials(mountedR, reg)
	MountDestinations(mountedR, reg)
	MountEvents(mountedR, reg)
	MountModules(mountedR, kubeC, "default")
	// mTLS material is intentionally empty: capture-file's download proxy
	// degrades to 503 "agent mTLS not configured" rather than dialing a
	// real sidecar, which envtest (apiserver+etcd only, no pods) can't
	// provide anyway.
	MountCapture(mountedR, reg, captureAuditor, CaptureConfig{
		DefaultRetentionSeconds: 86400,
		MaxRetentionSeconds:     604800,
		DefaultMaxDurationSecs:  300,
		DefaultMaxSizeBytes:     943718400, // 900 MiB, matching production (charts/gameplane/values.yaml)
	}, "", "", "")
	// Config routes require the database and auditor.
	MountConfig(mountedR, captureAuditStore, captureAuditor, false, "", nil)

	apiSrv = httptest.NewServer(mountedR)
	apiBase = apiSrv.URL

	code := m.Run()

	apiSrv.Close()
	_ = captureAuditStore.Close()
	_ = testEnv.Stop()
	os.Exit(code)
}

// uniqueResourceName generates a short, lowercase, K8s-DNS-friendly
// suffix on the given prefix. Tests share a namespace so names must
// not collide across tests.
func uniqueResourceName(prefix string) string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return prefix + "-" + hex.EncodeToString(buf)
}

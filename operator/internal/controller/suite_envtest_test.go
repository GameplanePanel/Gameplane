//go:build envtest

package controller

import (
	"os"
	"path/filepath"
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// envtest provides a real kube-apiserver + etcd in-process. It does NOT
// run the kube controllers (StatefulSet, Job, Pod, GC). Tests that need
// to drive a Job past Pending must Status().Update() the Job manually.
//
// The shared apiserver is bootstrapped here once. Per-test isolation
// comes from newNamespace(t) and per-test Manager goroutines started
// via startMgr(t, ...) — see helpers_envtest_test.go.

var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	scheme    *runtime.Scheme
)

func TestMain(m *testing.M) {
	scheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gameplanev1alpha1.AddToScheme(scheme))
	utilruntime.Must(snapshotv1.AddToScheme(scheme))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd"),
			// Vendored upstream CSI snapshot CRDs so the volume-snapshot
			// backup/restore paths can create real VolumeSnapshot objects.
			filepath.Join("..", "..", "test", "crds", "snapshot"),
		},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic("envtest: start apiserver: " + err.Error())
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		_ = testEnv.Stop()
		panic("envtest: build client: " + err.Error())
	}

	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

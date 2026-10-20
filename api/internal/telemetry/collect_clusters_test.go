package telemetry

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// TestCollect_SumsReachableClustersAndSkipsUnreachable checks that the counts
// and the environment cover every reachable registered cluster, and that a
// cluster whose list fails is left out (SC-018).
func TestCollect_SumsReachableClustersAndSkipsUnreachable(t *testing.T) {
	local := collectKubeClients(t, collectOpts{
		nodes:      []corev1.Node{testNode("l1", nil, "", "Ubuntu 24.04", "amd64")},
		serverInfo: &version.Info{Major: "1", Minor: "31", GitVersion: "v1.31.2+k3s1"},
		dynamic: []runtime.Object{
			serverObj("ls1", "lt", false, "", false),
			templateObj("lt", nil),
		},
	})
	remote := collectKubeClients(t, collectOpts{
		nodes: []corev1.Node{
			testNode("r1", nil, "", "Amazon Linux 2023", "arm64"),
			testNode("r2", nil, "", "Amazon Linux 2023", "arm64"),
		},
		serverInfo: &version.Info{Major: "1", Minor: "30", GitVersion: "v1.30.4-eks-abc"},
		dynamic: []runtime.Object{
			serverObj("rs1", "rt", false, "", false),
			serverObj("rs2", "rt", false, "", false),
			templateObj("rt", nil),
			gpObject("BackupSchedule", "nightly", "gameplane-games"),
		},
	})
	dead := collectKubeClients(t, collectOpts{
		nodesFail:   true,
		versionFail: true,
		dynamicFail: map[string]bool{"list": true},
	})

	reg := kube.NewRegistry("local")
	reg.Set("local", local)
	reg.Set("remote-a", remote)
	reg.Set("remote-b", dead)

	store := collectStore(t, true)
	deps := Deps{Kube: local, Registry: reg, Store: store, Version: "v1", Extended: true, Now: fixedNow}
	rep, err := Collect(t.Context(), deps)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Servers != 3 || rep.Templates != 2 {
		t.Fatalf("counts = %d servers, %d templates; want 3 and 2 (the dead cluster is skipped)", rep.Servers, rep.Templates)
	}
	if rep.Ext == nil {
		t.Fatal("missing extended part")
	}
	wantEnv := telemetryschema.Env{
		K8s:        []string{"1.30", "1.31"},
		Distro:     []string{"eks", "k3s"},
		Arch:       []string{"amd64", "arm64"},
		Nodes:      telemetryschema.NodeBand(3),
		Deployment: "cluster",
	}
	if !reflect.DeepEqual(rep.Ext.Env, wantEnv) {
		t.Errorf("env = %+v, want %+v", rep.Ext.Env, wantEnv)
	}
	if !rep.Ext.Features.Backups {
		t.Error("a backup schedule on a reachable cluster must set backups")
	}
	if rep.Ext.Games.Custom != 3 {
		t.Errorf("games.custom = %d, want 3", rep.Ext.Games.Custom)
	}
	if rep.Ext.Schema != 2 {
		t.Errorf("schema = %d, want 2", rep.Ext.Schema)
	}

	basic, err := Collect(t.Context(), Deps{Kube: local, Registry: reg, Store: store, Version: "v1", Extended: false, Now: fixedNow})
	if err != nil {
		t.Fatalf("basic collect: %v", err)
	}
	if basic.Servers != 3 || basic.Templates != 2 {
		t.Fatalf("basic counts = %d servers, %d templates; want 3 and 2", basic.Servers, basic.Templates)
	}
	if basic.Ext != nil {
		t.Fatal("the extended part must be absent when Extended is false")
	}
}

// TestCollect_StandaloneMarksDeploymentAndCountsRegisteredOnly checks that a
// standalone panel reports deployment "standalone" and counts only the
// registered clusters, never its own management client.
func TestCollect_StandaloneMarksDeploymentAndCountsRegisteredOnly(t *testing.T) {
	mgmt := &kube.Client{ClusterStore: collectKubeClients(t, collectOpts{}).Dynamic.Resource(kube.GVRCluster)}

	t.Run("registered remote is counted", func(t *testing.T) {
		remote := collectKubeClients(t, collectOpts{
			nodes:      []corev1.Node{testNode("r1", nil, "", "Ubuntu 24.04", "amd64")},
			serverInfo: &version.Info{Major: "1", Minor: "31", GitVersion: "v1.31.2+k3s1"},
			dynamic: []runtime.Object{
				serverObj("rs1", "rt", false, "", false),
				templateObj("rt", nil),
			},
		})
		reg := kube.NewRegistry("remote")
		reg.Set("remote", remote)

		rep, err := Collect(t.Context(), Deps{
			Kube: mgmt, Registry: reg, Store: collectStore(t, true), Version: "v1", Extended: true, Now: fixedNow,
		})
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if rep.Servers != 1 || rep.Templates != 1 {
			t.Fatalf("counts = %d servers, %d templates; want 1 and 1", rep.Servers, rep.Templates)
		}
		if rep.Ext == nil {
			t.Fatal("missing extended part")
		}
		if rep.Ext.Env.Deployment != "standalone" {
			t.Errorf("deployment = %q, want standalone", rep.Ext.Env.Deployment)
		}
		if !reflect.DeepEqual(rep.Ext.Env.K8s, []string{"1.31"}) {
			t.Errorf("k8s = %v, want [1.31]", rep.Ext.Env.K8s)
		}
	})

	t.Run("empty registry reports fallbacks", func(t *testing.T) {
		rep, err := Collect(t.Context(), Deps{
			Kube: mgmt, Registry: kube.NewRegistry("local"), Store: collectStore(t, true),
			Version: "v1", Extended: true, Now: fixedNow,
		})
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if rep.Servers != 0 || rep.Templates != 0 {
			t.Fatalf("counts = %d servers, %d templates; want 0 and 0", rep.Servers, rep.Templates)
		}
		if rep.Ext == nil {
			t.Fatal("missing extended part")
		}
		want := telemetryschema.Env{
			K8s:        []string{"other"},
			Distro:     []string{"other"},
			Arch:       []string{"other"},
			Nodes:      "other",
			Deployment: "standalone",
		}
		if !reflect.DeepEqual(rep.Ext.Env, want) {
			t.Errorf("env = %+v, want %+v", rep.Ext.Env, want)
		}
	})
}

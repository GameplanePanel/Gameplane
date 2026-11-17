package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	kubetesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

const inventoryPoolAnnotationFixture = `{"provider":"openebs-lvm","namespace":"openebs","volumeGroup":"openebs-chicago-1","thinPool":"openebs-chicago-1_thinpool","storageClasses":["openebs-lvm"]}`

// Wire assertions intentionally do not reference new production helpers or fields:
// this regression patch must compile against the unchanged v0.4.1 handlers.
type inventoryPoolStatsWire struct {
	Nodes                 int    `json:"nodes"`
	TotalStorageBytes     int64  `json:"totalStorageBytes"`
	UsedStorageBytes      int64  `json:"usedStorageBytes"`
	StorageCapacitySource string `json:"storageCapacitySource"`
}

type inventoryPoolFleetWire struct {
	Items []struct {
		Cluster string                 `json:"cluster"`
		Stats   inventoryPoolStatsWire `json:"stats"`
	} `json:"items"`
	Partial bool `json:"partial"`
	Issues  []struct {
		Cluster string `json:"cluster"`
		Code    string `json:"code"`
	} `json:"issues"`
}

func inventoryPoolNodeFixture(name, annotation string) *corev1.Node {
	node := readyNode(name, true, "4", "8Gi") // Reports system EPHEMERAL = 100Gi.
	node.Labels["openebs.io/nodename"] = name
	node.Labels["kubernetes.io/hostname"] = name
	if annotation != "" {
		node.Annotations = map[string]string{"gameplane.local/storage-pool": annotation}
	}
	return node
}

func inventoryPoolObjectFixture(name string, size any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "local.openebs.io/v1alpha1", "kind": "LVMNode",
		"metadata": map[string]any{"name": name, "namespace": "openebs"},
		// This is the real OpenEBS shape: volumeGroups is top-level, not status/spec.
		"volumeGroups": []any{map[string]any{
			"name": "openebs-chicago-1", "size": "372336Mi", "free": "42608Mi",
			"thinPools": []any{map[string]any{"name": "openebs-chicago-1_thinpool", "size": size, "free": "300Gi"}},
		}},
	}}
}

func inventoryPoolPVFixture(name, size, class, vg, node string, phase corev1.PersistentVolumePhase) *corev1.PersistentVolume {
	pv := boundPV(name, size, true)
	pv.Status.Phase = phase
	pv.Spec.StorageClassName = class
	pv.Spec.CSI = &corev1.CSIPersistentVolumeSource{
		Driver: "local.csi.openebs.io", VolumeHandle: name,
		VolumeAttributes: map[string]string{"openebs.io/volgroup": vg},
	}
	pv.Spec.NodeAffinity = &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "openebs.io/nodename", Operator: corev1.NodeSelectorOpIn, Values: []string{node}}},
	}}}}
	return pv
}

func inventoryPoolClientFixture(annotation string, pool *unstructured.Unstructured, extras ...runtime.Object) *kube.Client {
	var dynamicObjects []runtime.Object
	if pool != nil {
		dynamicObjects = append(dynamicObjects, pool)
	}
	client := fleetTestClient(dynamicObjects...)
	objects := []runtime.Object{
		inventoryPoolNodeFixture("pool-node", annotation),
		inventoryPoolPVFixture("selected", "7Gi", "openebs-lvm", "openebs-chicago-1", "pool-node", corev1.VolumeBound),
	}
	client.Typed = kubefake.NewClientset(append(objects, extras...)...)
	return client
}

func inventoryPoolRequestFixture(t *testing.T, route string, remote *kube.Client, user *auth.User) (*httptest.ResponseRecorder, *kube.Client) {
	t.Helper()
	home := fleetTestClient(newCluster("remote", nil, nil))
	home.Typed = kubefake.NewClientset(readyNode("home-node", true, "4", "8Gi"), boundPV("home-only", "91Gi", true))
	reg := clusterTestRegistry(home)
	reg.Set("remote", remote)
	router := chi.NewRouter()
	router.Use(rbac.Middleware(reg))
	MountCluster(router, reg, nil, "test", false, "")
	MountFleet(router, reg, nil)
	return inventoryRequest(t, router, user, http.MethodGet, route+"?cluster=remote"), home
}

func inventoryPoolDecodeFixture(t *testing.T, route string, rr *httptest.ResponseRecorder) inventoryPoolStatsWire {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", route, rr.Code, rr.Body)
	}
	if route == "/cluster/stats" {
		var stats inventoryPoolStatsWire
		if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
			t.Fatal(err)
		}
		return stats
	}
	var fleet inventoryPoolFleetWire
	if err := json.Unmarshal(rr.Body.Bytes(), &fleet); err != nil {
		t.Fatal(err)
	}
	if fleet.Partial || len(fleet.Items) != 1 || fleet.Items[0].Cluster != "remote" {
		t.Fatalf("unexpected inventory coverage: %+v", fleet)
	}
	return fleet.Items[0].Stats
}

func inventoryPoolNoHomeReadFixture(t *testing.T, home *kube.Client) {
	t.Helper()
	if len(home.Typed.(*kubefake.Clientset).Actions()) != 0 {
		t.Fatal("selected remote storage queried Home node/PV inventory")
	}
	for _, action := range home.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
		if action.GetResource().Resource == "lvmnodes" {
			t.Fatal("selected remote storage queried Home LVMNode")
		}
	}
}

func inventoryPoolUnavailableFixture(t *testing.T, route string, rr *httptest.ResponseRecorder) {
	t.Helper()
	if route == "/cluster/stats" {
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("want unavailable stats: status=%d body=%s", rr.Code, rr.Body)
		}
	} else {
		var fleet inventoryPoolFleetWire
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &fleet) != nil || !fleet.Partial || len(fleet.Items) != 0 || len(fleet.Issues) != 1 || fleet.Issues[0].Cluster != "remote" || fleet.Issues[0].Code != "unavailable" {
			t.Fatalf("want partial unavailable inventory: status=%d body=%s", rr.Code, rr.Body)
		}
	}
	for _, detail := range []string{"pool-node", "second-node", "openebs-chicago-1"} {
		if strings.Contains(rr.Body.String(), detail) {
			t.Fatalf("unavailable response leaked placement details: %s", rr.Body)
		}
	}
}

func inventoryPoolOnlyNamedReadFixture(t *testing.T, client *kube.Client, node string) {
	t.Helper()
	lookups := 0
	for _, action := range client.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
		if action.GetResource().Resource != "lvmnodes" {
			continue
		}
		get, ok := action.(kubetesting.GetAction)
		if !ok || action.GetVerb() != "get" || action.GetNamespace() != "openebs" || get.GetName() != node {
			t.Fatalf("pool read exceeded selected named GET: %v", action)
		}
		lookups++
	}
	if lookups != 1 {
		t.Fatalf("want one named pool GET, got %d", lookups)
	}
}

func TestStoragePoolInventory_SelectedPoolAndPVScope(t *testing.T) {
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, quantity := range []struct {
			name string
			size any
		}{{"quantity string", "320Gi"}, {"integer bytes", int64(320 << 30)}} {
			t.Run(route+"/"+quantity.name, func(t *testing.T) {
				otherDriver := inventoryPoolPVFixture("other-driver", "19Gi", "openebs-lvm", "openebs-chicago-1", "pool-node", corev1.VolumeBound)
				otherDriver.Spec.CSI.Driver = "unrelated.csi.example"
				pool := inventoryPoolObjectFixture("pool-node", quantity.size)
				pool.Object["volumeGroups"] = append([]any{map[string]any{"name": "unrelated-vg", "size": "600Gi"}}, pool.Object["volumeGroups"].([]any)...)
				remote := inventoryPoolClientFixture(inventoryPoolAnnotationFixture, pool,
					inventoryPoolPVFixture("released", "11Gi", "openebs-lvm", "openebs-chicago-1", "pool-node", corev1.VolumeReleased),
					inventoryPoolPVFixture("wrong-class", "13Gi", "another-class", "openebs-chicago-1", "pool-node", corev1.VolumeBound),
					inventoryPoolPVFixture("wrong-vg", "17Gi", "openebs-lvm", "another-vg", "pool-node", corev1.VolumeBound),
					inventoryPoolPVFixture("wrong-node", "23Gi", "openebs-lvm", "openebs-chicago-1", "another-node", corev1.VolumeBound), otherDriver)
				rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
				stats := inventoryPoolDecodeFixture(t, route, rr)
				if stats.TotalStorageBytes != 320<<30 || stats.UsedStorageBytes != 7<<30 || stats.StorageCapacitySource != "openebs-lvm" {
					t.Fatalf("want 320Gi pool/7Gi provisioned, not 100Gi EPHEMERAL or 20Gi physical-used: %+v", stats)
				}
				var lookups int
				for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
					if action.GetResource().Resource != "lvmnodes" {
						continue
					}
					get, ok := action.(kubetesting.GetAction)
					if !ok || action.GetNamespace() != "openebs" || get.GetName() != "pool-node" {
						t.Fatalf("pool discovery exceeded exact named GET: %v", action)
					}
					lookups++
				}
				if lookups == 0 {
					t.Fatal("configured capacity did not read the named LVMNode")
				}
				inventoryPoolNoHomeReadFixture(t, home)
			})
		}
	}
}

func TestStoragePoolInventory_WholeByteQuantities(t *testing.T) {
	const annotation = `{"provider":"openebs-lvm","namespace":"openebs","volumeGroup":"game-vg","thinPool":"game-vg_thinpool","storageClasses":["games"]}`
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, quantity := range []struct {
			name string
			size string
			want int64
		}{
			{"fractional binary unit", "1.5Gi", 1610612736},
			{"decimal binary mantissa", "320.0Gi", 343597383680},
			{"large canonical binary unit", "128Ti", 140737488355328},
			{"integer beyond float precision", "9007199254740993", 9007199254740993},
			{"maximum integer bytes", "9223372036854775807", 9223372036854775807},
			{"maximum binary bytes", "9007199254740991.9990234375Ki", 9223372036854775807},
		} {
			for _, field := range []string{"pool", "PV"} {
				t.Run(route+"/"+quantity.name+"/"+field, func(t *testing.T) {
					// Exercise pool and PV conversion independently: the other input
					// keeps a known-good quantity so one failure cannot hide the other.
					poolSize, pvSize := "320Gi", "7Gi"
					wantCapacity, wantProvisioned := int64(343597383680), int64(7516192768)
					if field == "pool" {
						poolSize, wantCapacity = quantity.size, quantity.want
					} else {
						pvSize, wantProvisioned = quantity.size, quantity.want
					}
					remote := fleetTestClient(inventoryNamedPoolObjectFixture("worker-1", "game-vg", "game-vg_thinpool", poolSize))
					remote.Typed = kubefake.NewClientset(
						inventoryPoolNodeFixture("worker-1", annotation),
						inventoryPoolPVFixture("game-data", pvSize, "games", "game-vg", "worker-1", corev1.VolumeBound),
					)
					rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
					stats := inventoryPoolDecodeFixture(t, route, rr)
					if stats.TotalStorageBytes != wantCapacity || stats.UsedStorageBytes != wantProvisioned || stats.StorageCapacitySource != "openebs-lvm" {
						t.Fatalf("whole-byte %s quantity %q: got %+v, want capacity=%d provisioned=%d", field, quantity.size, stats, wantCapacity, wantProvisioned)
					}
					inventoryPoolNoHomeReadFixture(t, home)
				})
			}
		}
	}
}

func inventoryNamedPoolObjectFixture(node, volumeGroup, thinPool, size string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "local.openebs.io/v1alpha1", "kind": "LVMNode",
		"metadata": map[string]any{"name": node, "namespace": "openebs"},
		"volumeGroups": []any{map[string]any{
			"name":      volumeGroup,
			"thinPools": []any{map[string]any{"name": thinPool, "size": size}},
		}},
	}}
}

func TestStoragePoolInventory_NonconventionalExistingPoolRejected(t *testing.T) {
	const annotation = `{"provider":"openebs-lvm","namespace":"openebs","volumeGroup":"game-vg","thinPool":"archive-pool","storageClasses":["games"]}`
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		t.Run(route, func(t *testing.T) {
			// The referenced alternate pool really exists and has valid capacity.
			// Reject its name before lookup: matching PV metadata cannot establish
			// that it uses this pool rather than OpenEBS's conventional thin pool.
			remote := fleetTestClient(inventoryNamedPoolObjectFixture("worker-1", "game-vg", "archive-pool", "320Gi"))
			remote.Typed = kubefake.NewClientset(
				inventoryPoolNodeFixture("worker-1", annotation),
				inventoryPoolPVFixture("game-data", "7Gi", "games", "game-vg", "worker-1", corev1.VolumeBound),
			)
			rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
			if route == "/cluster/stats" {
				if rr.Code != http.StatusServiceUnavailable {
					t.Fatalf("nonconventional existing pool must be unavailable: status=%d body=%s", rr.Code, rr.Body)
				}
			} else {
				var fleet inventoryPoolFleetWire
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &fleet) != nil || !fleet.Partial || len(fleet.Items) != 0 || len(fleet.Issues) != 1 || fleet.Issues[0].Cluster != "remote" || fleet.Issues[0].Code != "unavailable" {
					t.Fatalf("nonconventional existing pool lost unavailable coverage: status=%d body=%s", rr.Code, rr.Body)
				}
			}
			if actions := remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions(); len(actions) != 0 {
				t.Fatalf("nonconventional reference reached storage backend: %v", actions)
			}
			for _, detail := range []string{"archive-pool", "game-vg", "worker-1"} {
				if strings.Contains(rr.Body.String(), detail) {
					t.Fatalf("invalid reference leaked pool details: %s", rr.Body)
				}
			}
			inventoryPoolNoHomeReadFixture(t, home)
		})
	}
}

func TestStoragePoolInventory_MultipleNodesAggregateSelectedPools(t *testing.T) {
	const annotation = `{"provider":"openebs-lvm","namespace":"openebs","volumeGroup":"game-vg","thinPool":"game-vg_thinpool","storageClasses":["games"]}`
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		t.Run(route, func(t *testing.T) {
			firstPV := inventoryPoolPVFixture("first-data", "7Gi", "games", "game-vg", "worker-1", corev1.VolumeBound)
			// Two OR terms matching the same node must still count this PV once.
			firstPV.Spec.NodeAffinity.Required.NodeSelectorTerms = append(firstPV.Spec.NodeAffinity.Required.NodeSelectorTerms, corev1.NodeSelectorTerm{
				MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"worker-1"}}},
			})
			remote := fleetTestClient(
				inventoryNamedPoolObjectFixture("worker-1", "game-vg", "game-vg_thinpool", "320Gi"),
				inventoryNamedPoolObjectFixture("worker-2", "game-vg", "game-vg_thinpool", "480Gi"),
				inventoryNamedPoolObjectFixture("worker-3", "game-vg", "game-vg_thinpool", "900Gi"),
			)
			remote.Typed = kubefake.NewClientset(
				inventoryPoolNodeFixture("worker-1", annotation),
				inventoryPoolNodeFixture("worker-2", annotation),
				inventoryPoolNodeFixture("worker-3", ""),
				firstPV,
				inventoryPoolPVFixture("second-data", "11Gi", "games", "game-vg", "worker-2", corev1.VolumeBound),
				inventoryPoolPVFixture("unselected-data", "29Gi", "games", "game-vg", "worker-3", corev1.VolumeBound),
			)
			rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
			stats := inventoryPoolDecodeFixture(t, route, rr)
			if stats.Nodes != 3 || stats.TotalStorageBytes != 800<<30 || stats.UsedStorageBytes != 18<<30 || stats.StorageCapacitySource != "openebs-lvm" {
				t.Fatalf("want all 3 nodes but only 800Gi selected pools/18Gi provisioned counted once: %+v", stats)
			}
			lookups := map[string]int{}
			for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if action.GetResource().Resource != "lvmnodes" {
					continue
				}
				get, ok := action.(kubetesting.GetAction)
				if !ok || action.GetVerb() != "get" || action.GetNamespace() != "openebs" || (get.GetName() != "worker-1" && get.GetName() != "worker-2") {
					t.Fatalf("pool read exceeded selected named GETs: %v", action)
				}
				lookups[get.GetName()]++
			}
			if len(lookups) != 2 || lookups["worker-1"] != 1 || lookups["worker-2"] != 1 {
				t.Fatalf("want one GET per selected node and none for the unannotated node: %v", lookups)
			}
			inventoryPoolNoHomeReadFixture(t, home)
		})
	}
}

func TestStoragePoolInventory_ConfiguredFailuresDoNotFallback(t *testing.T) {
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, tc := range []struct {
			name       string
			annotation string
			modify     func(*unstructured.Unstructured)
			lookupErr  error
		}{
			{name: "malformed annotation", annotation: "{"},
			{name: "unsupported provider", annotation: strings.Replace(inventoryPoolAnnotationFixture, "openebs-lvm\"", "unsupported\"", 1)},
			{name: "case-variant annotation alias", annotation: strings.Replace(inventoryPoolAnnotationFixture, "{", "{\"Provider\":\"openebs-lvm\",", 1)},
			{name: "duplicate annotation key", annotation: strings.Replace(inventoryPoolAnnotationFixture, "{", "{\"provider\":\"openebs-lvm\",", 1)},
			{name: "unknown field", annotation: strings.Replace(inventoryPoolAnnotationFixture, "{", "{\"unexpected\":true,", 1)},
			{name: "empty classes", annotation: strings.Replace(inventoryPoolAnnotationFixture, "[\"openebs-lvm\"]", "[]", 1)},
			{name: "oversized annotation", annotation: strings.Repeat("x", 4097)},
			{name: "nonobject annotation", annotation: "[]"},
			{name: "truncated annotation value", annotation: `{"provider":`},
			{name: "trailing JSON", annotation: inventoryPoolAnnotationFixture + "{}"},
			{name: "wrong class field type", annotation: strings.Replace(inventoryPoolAnnotationFixture, `["openebs-lvm"]`, `"openebs-lvm"`, 1)},
			{name: "invalid class name", annotation: strings.Replace(inventoryPoolAnnotationFixture, `["openebs-lvm"]`, `["Invalid_Class"]`, 1)},
			{name: "duplicate classes", annotation: strings.Replace(inventoryPoolAnnotationFixture, `["openebs-lvm"]`, `["openebs-lvm","openebs-lvm"]`, 1)},
			{name: "nonarray volume groups", modify: func(p *unstructured.Unstructured) { p.Object["volumeGroups"] = map[string]any{} }},
			{name: "nonobject volume group", modify: func(p *unstructured.Unstructured) { p.Object["volumeGroups"] = []any{"invalid"} }},
			{name: "nonarray thin pools", modify: func(p *unstructured.Unstructured) {
				p.Object["volumeGroups"].([]any)[0].(map[string]any)["thinPools"] = map[string]any{}
			}},
			{name: "nonobject thin pool", modify: func(p *unstructured.Unstructured) {
				p.Object["volumeGroups"].([]any)[0].(map[string]any)["thinPools"] = []any{"invalid"}
			}},
			{name: "invalid size field type", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, true) }},
			{name: "invalid quantity", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "not-a-quantity") }},
			{name: "zero capacity", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "0") }},
			{name: "negative pool capacity", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "-1Gi") }},
			{name: "fractional pool bytes", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "500m") }},
			{name: "fractional binary pool bytes", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "0.1Ki") }},
			{name: "raw binary pool overflow", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "8Ei") }},
			{name: "fractional binary pool at maximum", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "9007199254740991.9990234374999Ki") }},
			{name: "quantity overflow", modify: func(p *unstructured.Unstructured) { inventoryPoolSizeFixture(p, "9223372036854775808") }},
			{name: "missing thin pool", modify: func(p *unstructured.Unstructured) { p.Object["volumeGroups"] = []any{} }},
			{name: "duplicate matching VG", modify: func(p *unstructured.Unstructured) {
				groups := p.Object["volumeGroups"].([]any)
				p.Object["volumeGroups"] = append(groups, runtime.DeepCopyJSONValue(groups[0]))
			}},
			{name: "duplicate matching pool", modify: func(p *unstructured.Unstructured) {
				group := p.Object["volumeGroups"].([]any)[0].(map[string]any)
				pools := group["thinPools"].([]any)
				group["thinPools"] = append(pools, runtime.DeepCopyJSONValue(pools[0]))
			}},
			{name: "forbidden lookup", lookupErr: apierrors.NewForbidden(schema.GroupResource{Group: "local.openebs.io", Resource: "lvmnodes"}, "pool-node", errors.New("private-pool-detail"))},
			{name: "missing LVMNode", lookupErr: apierrors.NewNotFound(schema.GroupResource{Group: "local.openebs.io", Resource: "lvmnodes"}, "pool-node")},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				annotation := tc.annotation
				if annotation == "" {
					annotation = inventoryPoolAnnotationFixture
				}
				pool := inventoryPoolObjectFixture("pool-node", "320Gi")
				if tc.modify != nil {
					tc.modify(pool)
				}
				remote := inventoryPoolClientFixture(annotation, pool)
				if tc.lookupErr != nil {
					remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "lvmnodes", func(kubetesting.Action) (bool, runtime.Object, error) {
						return true, nil, tc.lookupErr
					})
				}
				rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
				if route == "/cluster/stats" {
					if rr.Code == http.StatusOK {
						t.Fatalf("configured failure became known node/zero capacity: %s", rr.Body)
					}
				} else {
					var fleet inventoryPoolFleetWire
					if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &fleet) != nil || !fleet.Partial || len(fleet.Items) != 0 || len(fleet.Issues) == 0 || fleet.Issues[0].Cluster != "remote" {
						t.Fatalf("configured failure did not preserve unavailable fleet coverage: status=%d body=%s", rr.Code, rr.Body)
					}
				}
				if strings.Contains(rr.Body.String(), "private-pool-detail") {
					t.Fatal("pool lookup leaked upstream details")
				}
				inventoryPoolNoHomeReadFixture(t, home)
			})
		}
	}
}

func inventoryPoolSizeFixture(pool *unstructured.Unstructured, value any) {
	pool.Object["volumeGroups"].([]any)[0].(map[string]any)["thinPools"].([]any)[0].(map[string]any)["size"] = value
}

func TestStoragePoolInventory_SummedCapacityOverflowUnavailable(t *testing.T) {
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		t.Run(route, func(t *testing.T) {
			// Each 1E quantity is exactly representable; ten pools overflow int64
			// only at aggregation, rather than failing an individual parse.
			remote := inventoryPoolClientFixture(inventoryPoolAnnotationFixture, inventoryPoolObjectFixture("pool-node", "1E"))
			for i := range 9 {
				name := fmt.Sprintf("extra-node-%d", i)
				if _, err := remote.Typed.CoreV1().Nodes().Create(t.Context(), inventoryPoolNodeFixture(name, inventoryPoolAnnotationFixture), metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := remote.Dynamic.(*dynamicfake.FakeDynamicClient).Tracker().Add(inventoryPoolObjectFixture(name, "1E")); err != nil {
					t.Fatal(err)
				}
			}
			rr, _ := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
			if route == "/cluster/stats" {
				if rr.Code == http.StatusOK {
					t.Fatalf("overflowing pool sum became known capacity: %s", rr.Body)
				}
			} else {
				var fleet inventoryPoolFleetWire
				if err := json.Unmarshal(rr.Body.Bytes(), &fleet); err != nil || !fleet.Partial || len(fleet.Items) != 0 {
					t.Fatalf("overflowing pool sum lost unknown coverage: %s", rr.Body)
				}
			}
		})
	}
}

func TestStoragePoolInventory_RequiresClusterInventoryAuthority(t *testing.T) {
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		t.Run(route, func(t *testing.T) {
			remote := inventoryPoolClientFixture(inventoryPoolAnnotationFixture, inventoryPoolObjectFixture("pool-node", "320Gi"))
			rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "gameplane-games", "*"))
			if route == "/cluster/stats" && rr.Code != http.StatusForbidden {
				t.Fatalf("namespace grant read pool inventory: %d", rr.Code)
			}
			if route == "/fleet/inventory" {
				var fleet inventoryPoolFleetWire
				if err := json.Unmarshal(rr.Body.Bytes(), &fleet); err != nil || len(fleet.Items) != 0 || fleet.Partial {
					t.Fatalf("namespace grant probed pool inventory: %s", rr.Body)
				}
			}
			if len(remote.Typed.(*kubefake.Clientset).Actions()) != 0 || len(remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions()) != 0 {
				t.Fatal("namespace-only authority triggered a pool or node/PV lookup")
			}
			inventoryPoolNoHomeReadFixture(t, home)
		})
	}
}

func TestStoragePoolInventory_LegacyUnconfigured(t *testing.T) {
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		t.Run(route, func(t *testing.T) {
			remote := inventoryPoolClientFixture("", inventoryPoolObjectFixture("pool-node", "320Gi"))
			rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
			stats := inventoryPoolDecodeFixture(t, route, rr)
			if stats.TotalStorageBytes != 100<<30 || stats.UsedStorageBytes != 7<<30 || stats.StorageCapacitySource != "node-ephemeral" {
				t.Fatalf("unconfigured inventory changed legacy calculation/source: %+v", stats)
			}
			for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
				if action.GetResource().Resource == "lvmnodes" {
					t.Fatal("unconfigured cluster read an unselected storage backend")
				}
			}
			inventoryPoolNoHomeReadFixture(t, home)
		})
	}
}

func TestStoragePoolInventory_NodeAffinityBooleanScope(t *testing.T) {
	in := func(key, value string) corev1.NodeSelectorRequirement {
		return corev1.NodeSelectorRequirement{Key: key, Operator: corev1.NodeSelectorOpIn, Values: []string{value}}
	}
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, tc := range []struct {
			name  string
			terms []corev1.NodeSelectorTerm
			want  int64
		}{
			{"OR terms", []corev1.NodeSelectorTerm{
				{MatchExpressions: []corev1.NodeSelectorRequirement{in("openebs.io/nodename", "other-node")}},
				{MatchExpressions: []corev1.NodeSelectorRequirement{in("openebs.io/nodename", "pool-node")}},
			}, 7 << 30},
			{"AND requirements", []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{
				in("openebs.io/nodename", "pool-node"), in("missing-label", "required"),
			}}}, 0},
			{"empty term matches no node", []corev1.NodeSelectorTerm{{}}, 0},
			{"metadata name field", []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{
				in("metadata.name", "pool-node"),
			}}}, 7 << 30},
			{"fields AND expressions", []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{in("openebs.io/nodename", "pool-node")},
				MatchFields:      []corev1.NodeSelectorRequirement{in("metadata.name", "other-node")},
			}}, 0},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				remote := inventoryPoolClientFixture(inventoryPoolAnnotationFixture, inventoryPoolObjectFixture("pool-node", "320Gi"))
				pv, err := remote.Typed.CoreV1().PersistentVolumes().Get(t.Context(), "selected", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pv.Spec.NodeAffinity.Required.NodeSelectorTerms = tc.terms
				if _, err := remote.Typed.CoreV1().PersistentVolumes().Update(t.Context(), pv, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				rr, _ := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
				stats := inventoryPoolDecodeFixture(t, route, rr)
				if stats.TotalStorageBytes != 320<<30 || stats.UsedStorageBytes != tc.want {
					t.Fatalf("affinity scope: %+v want provisioned=%d", stats, tc.want)
				}
			})
		}
	}
}

func TestStoragePoolInventory_LongNodeNameFieldAffinity(t *testing.T) {
	name := strings.Repeat("worker", 10) + ".example"
	otherName := strings.Repeat("other", 12) + ".example"
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, tc := range []struct {
			name     string
			operator corev1.NodeSelectorOperator
			value    string
			want     int64
		}{
			{"In matches", corev1.NodeSelectorOpIn, name, 7 << 30},
			{"In excludes", corev1.NodeSelectorOpIn, otherName, 0},
			{"NotIn matches", corev1.NodeSelectorOpNotIn, otherName, 7 << 30},
			{"NotIn excludes", corev1.NodeSelectorOpNotIn, name, 0},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				node := inventoryPoolNodeFixture(name, inventoryPoolAnnotationFixture)
				// Node names are DNS subdomains, while label values are limited
				// to 63 characters. Keep labels valid to isolate field matching.
				node.Labels = map[string]string{"openebs.io/nodename": "worker", "kubernetes.io/hostname": "worker"}
				pv := inventoryPoolPVFixture("game-data", "7Gi", "openebs-lvm", "openebs-chicago-1", "worker", corev1.VolumeBound)
				pv.Spec.NodeAffinity.Required.NodeSelectorTerms = []corev1.NodeSelectorTerm{{
					MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: tc.operator, Values: []string{tc.value}}},
				}}
				remote := fleetTestClient(inventoryPoolObjectFixture(name, "320Gi"))
				remote.Typed = kubefake.NewClientset(node, pv)
				rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
				stats := inventoryPoolDecodeFixture(t, route, rr)
				if stats.TotalStorageBytes != 320<<30 || stats.UsedStorageBytes != tc.want || stats.StorageCapacitySource != "openebs-lvm" {
					t.Fatalf("long node-name field affinity: got %+v, want provisioned=%d", stats, tc.want)
				}
				inventoryPoolOnlyNamedReadFixture(t, remote, name)
				inventoryPoolNoHomeReadFixture(t, home)
			})
		}
	}
}

func TestStoragePoolInventory_UnannotatedNodePlacementScope(t *testing.T) {
	term := func(names ...string) corev1.NodeSelectorTerm {
		return corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "openebs.io/nodename", Operator: corev1.NodeSelectorOpIn, Values: names}}}
	}
	broad := []corev1.NodeSelectorTerm{term("pool-node", "second-node")}
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, tc := range []struct {
			name        string
			terms       []corev1.NodeSelectorTerm
			modify      func(*corev1.PersistentVolume)
			unavailable bool
			want        int64
		}{
			{name: "label In selected and unannotated nodes", terms: broad, unavailable: true},
			{name: "OR selected and unannotated nodes", terms: []corev1.NodeSelectorTerm{term("pool-node"), term("second-node")}, unavailable: true},
			{name: "only unannotated node", terms: []corev1.NodeSelectorTerm{term("second-node")}},
			{name: "repeated OR selected node", terms: []corev1.NodeSelectorTerm{term("pool-node"), term("pool-node")}, want: 7 << 30},
			{name: "unrelated class broad affinity", terms: broad, modify: func(p *corev1.PersistentVolume) { p.Spec.StorageClassName = "other-class" }},
			{name: "unrelated VG broad affinity", terms: broad, modify: func(p *corev1.PersistentVolume) { p.Spec.CSI.VolumeAttributes["openebs.io/volgroup"] = "other-vg" }},
			{name: "unrelated driver broad affinity", terms: broad, modify: func(p *corev1.PersistentVolume) { p.Spec.CSI.Driver = "unrelated.csi.example" }},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				pv := inventoryPoolPVFixture("game-data", "7Gi", "openebs-lvm", "openebs-chicago-1", "pool-node", corev1.VolumeBound)
				pv.Spec.NodeAffinity.Required.NodeSelectorTerms = tc.terms
				if tc.modify != nil {
					tc.modify(pv)
				}
				remote := fleetTestClient(inventoryPoolObjectFixture("pool-node", "320Gi"))
				remote.Typed = kubefake.NewClientset(inventoryPoolNodeFixture("pool-node", inventoryPoolAnnotationFixture), inventoryPoolNodeFixture("second-node", ""), pv)
				rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
				if tc.unavailable {
					inventoryPoolUnavailableFixture(t, route, rr)
				} else {
					stats := inventoryPoolDecodeFixture(t, route, rr)
					if stats.Nodes != 2 || stats.TotalStorageBytes != 320<<30 || stats.UsedStorageBytes != tc.want || stats.StorageCapacitySource != "openebs-lvm" {
						t.Fatalf("unannotated node placement: got %+v, want provisioned=%d", stats, tc.want)
					}
				}
				inventoryPoolOnlyNamedReadFixture(t, remote, "pool-node")
				inventoryPoolNoHomeReadFixture(t, home)
			})
		}
	}
}

func TestStoragePoolInventory_InvalidVolumeMetadataUnavailable(t *testing.T) {
	field := func(operator corev1.NodeSelectorOperator, values ...string) func(*corev1.PersistentVolume) {
		return func(p *corev1.PersistentVolume) {
			p.Spec.NodeAffinity.Required.NodeSelectorTerms = []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: operator, Values: values}}}}
		}
	}
	for _, route := range []string{"/cluster/stats", "/fleet/inventory"} {
		for _, tc := range []struct {
			name    string
			modify  func(*corev1.PersistentVolume)
			prepare func(*testing.T, *kube.Client)
		}{
			{name: "missing affinity", modify: func(p *corev1.PersistentVolume) { p.Spec.NodeAffinity = nil }},
			{name: "missing required affinity", modify: func(p *corev1.PersistentVolume) { p.Spec.NodeAffinity.Required = nil }},
			{name: "invalid affinity operator", modify: func(p *corev1.PersistentVolume) {
				p.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Operator = "Invalid"
			}},
			{name: "invalid affinity requirement", modify: func(p *corev1.PersistentVolume) {
				p.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values = nil
			}},
			{name: "unsupported node field", modify: func(p *corev1.PersistentVolume) {
				p.Spec.NodeAffinity.Required.NodeSelectorTerms = []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.namespace", Operator: corev1.NodeSelectorOpIn, Values: []string{"invalid"}}}}}
			}},
			{name: "invalid node field requirement", modify: func(p *corev1.PersistentVolume) {
				p.Spec.NodeAffinity.Required.NodeSelectorTerms = []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn}}}}
			}},
			{name: "node field Exists operator", modify: field(corev1.NodeSelectorOpExists)},
			{name: "node field Gt operator", modify: field(corev1.NodeSelectorOpGt, "1")},
			{name: "node field In multiple values", modify: field(corev1.NodeSelectorOpIn, "pool-node", "second-node")},
			{name: "node field NotIn multiple values", modify: field(corev1.NodeSelectorOpNotIn, "other-node", "second-node")},
			{name: "node field NotIn missing value", modify: field(corev1.NodeSelectorOpNotIn)},
			{name: "negative PV capacity", modify: func(p *corev1.PersistentVolume) { p.Spec.Capacity[corev1.ResourceStorage] = resource.MustParse("-1Gi") }},
			{name: "fractional PV bytes", modify: func(p *corev1.PersistentVolume) { p.Spec.Capacity[corev1.ResourceStorage] = resource.MustParse("500m") }},
			{name: "unrepresentable PV capacity", modify: func(p *corev1.PersistentVolume) {
				p.Spec.Capacity[corev1.ResourceStorage] = resource.MustParse("9223372036854775808")
			}},
			{name: "provisioned sum overflow", prepare: func(t *testing.T, c *kube.Client) {
				t.Helper()
				for i := range 10 {
					pv := inventoryPoolPVFixture(fmt.Sprintf("huge-volume-%d", i), "1E", "openebs-lvm", "openebs-chicago-1", "pool-node", corev1.VolumeBound)
					if _, err := c.Typed.CoreV1().PersistentVolumes().Create(t.Context(), pv, metav1.CreateOptions{}); err != nil {
						t.Fatal(err)
					}
				}
			}},
			{name: "ambiguous local volume placement", modify: func(p *corev1.PersistentVolume) {
				p.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values = []string{"pool-node", "second-node"}
			}, prepare: func(t *testing.T, c *kube.Client) {
				t.Helper()
				if _, err := c.Typed.CoreV1().Nodes().Create(t.Context(), inventoryPoolNodeFixture("second-node", inventoryPoolAnnotationFixture), metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := c.Dynamic.(*dynamicfake.FakeDynamicClient).Tracker().Add(inventoryPoolObjectFixture("second-node", "320Gi")); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "missing dynamic source client", prepare: func(_ *testing.T, c *kube.Client) { c.Dynamic = nil }},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				remote := inventoryPoolClientFixture(inventoryPoolAnnotationFixture, inventoryPoolObjectFixture("pool-node", "320Gi"))
				if tc.modify != nil {
					pv, err := remote.Typed.CoreV1().PersistentVolumes().Get(t.Context(), "selected", metav1.GetOptions{})
					if err != nil {
						t.Fatal(err)
					}
					tc.modify(pv)
					if _, err := remote.Typed.CoreV1().PersistentVolumes().Update(t.Context(), pv, metav1.UpdateOptions{}); err != nil {
						t.Fatal(err)
					}
				}
				if tc.prepare != nil {
					tc.prepare(t, remote)
				}
				rr, home := inventoryPoolRequestFixture(t, route, remote, inventoryUser("remote", "*", "cluster:read"))
				if route == "/cluster/stats" {
					if rr.Code == http.StatusOK {
						t.Fatalf("unsafe volume metadata became known capacity: %s", rr.Body)
					}
				} else {
					var fleet inventoryPoolFleetWire
					if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &fleet) != nil || !fleet.Partial || len(fleet.Items) != 0 || len(fleet.Issues) == 0 {
						t.Fatalf("unsafe volume metadata lost unavailable coverage: status=%d body=%s", rr.Code, rr.Body)
					}
				}
				inventoryPoolNoHomeReadFixture(t, home)
			})
		}
	}
}

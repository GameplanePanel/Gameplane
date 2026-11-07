package kube

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNodeCaptureServiceDerivesOnlyBoundInternalName(t *testing.T) {
	nc := &NetworkCapture{ObjectMeta: metav1.ObjectMeta{UID: "11111111-2222-3333-4444-555555555555", Annotations: map[string]string{NodeCaptureNamespaceAnnotation: "diagnostics"}}}
	name, ns, node, err := NodeCaptureService(nc)
	if err != nil || !node || ns != "diagnostics" || name != "capture-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("unexpected target: %s %s %t %v", name, ns, node, err)
	}
	for _, invalid := range []string{"https://host", "../games", "host:9091", "UPPER"} {
		nc.Annotations[NodeCaptureNamespaceAnnotation] = invalid
		if _, _, _, err := NodeCaptureService(nc); err == nil {
			t.Fatalf("accepted namespace %q", invalid)
		}
	}
	nc.Annotations[NodeCaptureNamespaceAnnotation] = "diagnostics"
	nc.UID = "../../host"
	if _, _, _, err := NodeCaptureService(nc); err == nil {
		t.Fatal("accepted invalid capture UID")
	}
}

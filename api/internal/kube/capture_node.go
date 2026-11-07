package kube

import (
	"errors"
	"regexp"

	"k8s.io/apimachinery/pkg/util/validation"
)

// NodeCaptureNamespaceAnnotation is written only by the operator when it
// binds a NetworkCapture to a dedicated diagnostic artifact service.
const NodeCaptureNamespaceAnnotation = "gameplane.local/capture-node-namespace"

var nodeCaptureUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NodeCaptureService derives a fixed in-cluster service from the immutable
// capture UID. An annotation can never supply a URL, arbitrary host or path.
func NodeCaptureService(nc *NetworkCapture) (name, namespace string, node bool, err error) {
	namespace = nc.Annotations[NodeCaptureNamespaceAnnotation]
	if namespace == "" {
		return "", "", false, nil
	}
	if !nodeCaptureUID.MatchString(string(nc.UID)) || len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", "", true, errors.New("invalid diagnostic artifact identity")
	}
	return "capture-" + string(nc.UID), namespace, true, nil
}

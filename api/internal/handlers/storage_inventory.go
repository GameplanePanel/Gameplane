package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

const storagePoolAnnotation = "gameplane.local/storage-pool"

var storageLVMName = regexp.MustCompile(`^[A-Za-z0-9_+.][A-Za-z0-9_+.-]{0,126}$`)
var storageLVMNodeResource = schema.GroupVersionResource{Group: "local.openebs.io", Version: "v1alpha1", Resource: "lvmnodes"}

type storagePoolReference struct {
	Provider       string   `json:"provider"`
	Namespace      string   `json:"namespace"`
	VolumeGroup    string   `json:"volumeGroup"`
	ThinPool       string   `json:"thinPool"`
	StorageClasses []string `json:"storageClasses"`
}

type selectedStoragePool struct {
	reference storagePoolReference
	node      *corev1.Node
}

// resolveStorageInventory never writes to a node, volume or pool. Configured
// sources fail closed: their absence is not permission to substitute system disk.
func resolveStorageInventory(ctx context.Context, client *kube.Client, nodes []corev1.Node, pvs []corev1.PersistentVolume) (clusterStats, error) {
	stats := clusterStats{Nodes: len(nodes), StorageCapacitySource: "node-ephemeral"}
	var pools []selectedStoragePool
	seen := map[string]bool{}
	for i := range nodes {
		text, configured := nodes[i].Annotations[storagePoolAnnotation]
		if !configured {
			continue
		}
		reference, err := parseStoragePoolReference(text)
		if err != nil {
			return clusterStats{}, err
		}
		if seen[nodes[i].Name] {
			return clusterStats{}, errors.New("duplicate configured storage node")
		}
		seen[nodes[i].Name] = true
		pools = append(pools, selectedStoragePool{reference: reference, node: &nodes[i]})
	}
	if len(pools) == 0 {
		stats.TotalStorageBytes = nodeStorageCapacity(nodes)
		stats.UsedStorageBytes = boundVolumeBytes(pvs)
		return stats, nil
	}
	if client == nil || client.Dynamic == nil {
		return clusterStats{}, errors.New("configured storage source unavailable")
	}
	stats.StorageCapacitySource = "openebs-lvm"
	for _, pool := range pools {
		reference := pool.reference
		obj, err := client.Dynamic.Resource(storageLVMNodeResource).Namespace(reference.Namespace).Get(ctx, pool.node.Name, metav1.GetOptions{})
		if err != nil {
			return clusterStats{}, fmt.Errorf("read configured storage source: %w", err)
		}
		capacity, err := storagePoolCapacity(obj.Object["volumeGroups"], reference)
		if err != nil {
			return clusterStats{}, err
		}
		stats.TotalStorageBytes, err = addStorageCapacity(stats.TotalStorageBytes, capacity)
		if err != nil {
			return clusterStats{}, err
		}
	}
	for i := range pvs {
		pv := &pvs[i]
		if pv.Status.Phase != corev1.VolumeBound || pv.Spec.CSI == nil || pv.Spec.CSI.Driver != "local.csi.openebs.io" {
			continue
		}
		matches := 0
		for _, pool := range pools {
			if !storageClassSelected(pool.reference.StorageClasses, pv.Spec.StorageClassName) || pv.Spec.CSI.VolumeAttributes["openebs.io/volgroup"] != pool.reference.VolumeGroup {
				continue
			}
			matched, err := storagePVMatchesNode(pv, pool.node)
			if err != nil {
				return clusterStats{}, err
			}
			if matched {
				matches++
			}
		}
		if matches > 1 {
			return clusterStats{}, errors.New("ambiguous configured storage volume affinity")
		}
		if matches == 1 {
			quantity := pv.Spec.Capacity[corev1.ResourceStorage]
			value, exact := quantity.AsInt64()
			if !exact || value < 0 {
				return clusterStats{}, errors.New("invalid configured storage volume capacity")
			}
			var err error
			stats.UsedStorageBytes, err = addStorageCapacity(stats.UsedStorageBytes, value)
			if err != nil {
				return clusterStats{}, err
			}
		}
	}
	return stats, nil
}

func parseStoragePoolReference(text string) (storagePoolReference, error) {
	invalid := errors.New("invalid configured storage reference")
	if len(text) > 4096 {
		return storagePoolReference{}, invalid
	}
	// A struct decoder rejects unknown fields, but accepts duplicate keys. Check
	// the top-level object too; nested objects are not allowed by this schema.
	keys := json.NewDecoder(strings.NewReader(text))
	start, err := keys.Token()
	if err != nil || start != json.Delim('{') {
		return storagePoolReference{}, invalid
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"provider": true, "namespace": true, "volumeGroup": true, "thinPool": true, "storageClasses": true}
	for keys.More() {
		token, err := keys.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return storagePoolReference{}, invalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err := keys.Decode(&raw); err != nil {
			return storagePoolReference{}, invalid
		}
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var reference storagePoolReference
	if err := decoder.Decode(&reference); err != nil {
		return storagePoolReference{}, invalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return storagePoolReference{}, invalid
	}
	if reference.Provider != "openebs-lvm" || len(validation.IsDNS1123Label(reference.Namespace)) != 0 || !validStorageLVMName(reference.VolumeGroup) || !validStorageLVMName(reference.ThinPool) || len(reference.StorageClasses) == 0 {
		return storagePoolReference{}, invalid
	}
	classes := map[string]bool{}
	for _, name := range reference.StorageClasses {
		if len(validation.IsDNS1123Subdomain(name)) != 0 || classes[name] {
			return storagePoolReference{}, invalid
		}
		classes[name] = true
	}
	return reference, nil
}

func validStorageLVMName(name string) bool {
	return name != "." && name != ".." && storageLVMName.MatchString(name)
}

func storageClassSelected(classes []string, name string) bool {
	for _, candidate := range classes {
		if candidate == name {
			return true
		}
	}
	return false
}

func storagePoolCapacity(raw any, reference storagePoolReference) (int64, error) {
	invalid := errors.New("configured storage pool capacity unavailable")
	groups, ok := raw.([]any)
	if !ok {
		return 0, invalid
	}
	var size any
	matchedGroups, matchedPools := 0, 0
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			return 0, invalid
		}
		if group["name"] != reference.VolumeGroup {
			continue
		}
		matchedGroups++
		pools, ok := group["thinPools"].([]any)
		if !ok {
			return 0, invalid
		}
		for _, rawPool := range pools {
			pool, ok := rawPool.(map[string]any)
			if !ok {
				return 0, invalid
			}
			if pool["name"] == reference.ThinPool {
				matchedPools++
				size = pool["size"]
			}
		}
	}
	if matchedGroups != 1 || matchedPools != 1 {
		return 0, invalid
	}
	var text string
	switch value := size.(type) {
	case string:
		text = value
	case int64:
		text = strconv.FormatInt(value, 10)
	default:
		return 0, invalid
	}
	quantity, err := resource.ParseQuantity(text)
	if err != nil {
		return 0, invalid
	}
	value, exact := quantity.AsInt64()
	if !exact || value <= 0 {
		return 0, invalid
	}
	return value, nil
}

func addStorageCapacity(total, value int64) (int64, error) {
	if value < 0 || total > math.MaxInt64-value {
		return 0, errors.New("configured storage capacity overflow")
	}
	return total + value, nil
}

func storagePVMatchesNode(pv *corev1.PersistentVolume, node *corev1.Node) (bool, error) {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return false, errors.New("configured storage volume affinity unavailable")
	}
	matched := false
	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		if len(term.MatchExpressions)+len(term.MatchFields) == 0 {
			continue // Kubernetes defines an empty term as matching no nodes.
		}
		termMatches := true
		for _, expression := range term.MatchExpressions {
			ok, err := storageNodeRequirement(expression, labels.Set(node.Labels))
			if err != nil {
				return false, err
			}
			termMatches = termMatches && ok
		}
		for _, expression := range term.MatchFields {
			if expression.Key != "metadata.name" {
				return false, errors.New("invalid configured storage node affinity field")
			}
			ok, err := storageNodeRequirement(expression, labels.Set{"metadata.name": node.Name})
			if err != nil {
				return false, err
			}
			termMatches = termMatches && ok
		}
		matched = matched || termMatches
	}
	return matched, nil
}

func storageNodeRequirement(expression corev1.NodeSelectorRequirement, values labels.Set) (bool, error) {
	operators := map[corev1.NodeSelectorOperator]selection.Operator{
		corev1.NodeSelectorOpIn: selection.In, corev1.NodeSelectorOpNotIn: selection.NotIn,
		corev1.NodeSelectorOpExists: selection.Exists, corev1.NodeSelectorOpDoesNotExist: selection.DoesNotExist,
		corev1.NodeSelectorOpGt: selection.GreaterThan, corev1.NodeSelectorOpLt: selection.LessThan,
	}
	operator, ok := operators[expression.Operator]
	if !ok {
		return false, errors.New("invalid configured storage node affinity operator")
	}
	requirement, err := labels.NewRequirement(expression.Key, operator, expression.Values)
	if err != nil {
		return false, errors.New("invalid configured storage node affinity requirement")
	}
	return requirement.Matches(values), nil
}

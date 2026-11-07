package kube

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EffectiveTemplateRevisionAnnotation records the template admitted by the
// operator. Once set, consumers must not fall back to the current catalog.
const EffectiveTemplateRevisionAnnotation = "gameplane.local/effective-template-revision"

// EffectiveGameTemplate reads the schema used by this server, including an
// OnRequest server that has not adopted the latest catalog revision. Snapshot
// identity and content checks match the operator's snapshot contract; reads use
// this cluster's client and the server's namespace, never a caller-supplied path.
func (c *Client) EffectiveGameTemplate(ctx context.Context, gs *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if c == nil || gs == nil {
		return nil, fmt.Errorf("template client or server is missing")
	}
	name, _, err := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
	if err != nil || name == "" {
		return nil, fmt.Errorf("server template name is missing or malformed")
	}
	revision := gs.GetAnnotations()[EffectiveTemplateRevisionAnnotation]
	if revision == "" {
		if c.Dynamic == nil {
			return nil, fmt.Errorf("template client is missing")
		}
		return c.Dynamic.Resource(GVRs["templates"]).Get(ctx, name, metav1.GetOptions{})
	}
	decoded, err := hex.DecodeString(revision)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != revision {
		return nil, fmt.Errorf("invalid effective template revision")
	}
	if c.Typed == nil || gs.GetUID() == "" || gs.GetNamespace() == "" || gs.GetName() == "" {
		return nil, fmt.Errorf("snapshot client or server identity is missing")
	}
	identity := sha256.Sum256([]byte(string(gs.GetUID()) + "/" + gs.GetNamespace() + "/" + gs.GetName()))
	snapshotName := fmt.Sprintf("gpt-%x-%s", identity[:8], revision[:32])
	cm, err := c.Typed.CoreV1().ConfigMaps(gs.GetNamespace()).Get(ctx, snapshotName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("read pinned template snapshot: %w", err)
	}
	owner := metav1.GetControllerOf(cm)
	if cm.Name != snapshotName || cm.Namespace != gs.GetNamespace() || owner == nil ||
		owner.UID != gs.GetUID() || owner.Name != gs.GetName() || owner.Kind != "GameServer" || owner.APIVersion != "gameplane.local/v1alpha1" ||
		cm.Immutable == nil || !*cm.Immutable || !cm.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("template snapshot is foreign, mutable or deleting")
	}
	data := []byte(cm.Data["template.json"])
	if len(data) > 900*1024 {
		return nil, fmt.Errorf("template snapshot exceeds 900 KiB")
	}
	// The operator stores the exact JSON bytes it hashes. Hash those bytes
	// directly so the API does not need a second copy of the typed CRD schema.
	// Noncanonical rewrites are rejected even if they decode to the same spec.
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != revision {
		return nil, fmt.Errorf("template snapshot content does not match pinned revision")
	}
	var tmpl unstructured.Unstructured
	// The stored snapshot deliberately omits kind/apiVersion. Decode its map
	// directly rather than invoking Unstructured's GVK-requiring decoder.
	if err := json.Unmarshal(data, &tmpl.Object); err != nil {
		return nil, fmt.Errorf("decode template snapshot: %w", err)
	}
	if tmpl.GetName() != name {
		return nil, fmt.Errorf("template snapshot name does not match server")
	}
	return &tmpl, nil
}

package modsrc

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func uploadCM(name string, files map[string]string) *corev1.ConfigMap {
	binary := map[string][]byte{}
	for k, v := range files {
		binary[k] = []byte(v)
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "gameplane-system",
			Labels:    map[string]string{gameplanev1alpha1.LabelModuleUpload: "true"},
		},
		BinaryData: binary,
	}
}

func uploadClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().WithObjects(objs...).Build()
}

func TestUploadFetcher_IndexAndPull(t *testing.T) {
	c := uploadClient(
		uploadCM("bundle-mc", validModuleFiles("mc", "1.0.0")),
		// Unlabeled ConfigMaps in the namespace are ignored.
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "gameplane-system"},
			Data:       map[string]string{"foo": "bar"},
		},
	)
	f := newUpload(c, "gameplane-system", nil)

	entries, warnings, err := f.Index(context.Background())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Index: %v warnings=%v", err, warnings)
	}
	if len(entries) != 1 || entries[0].Name != "mc" || entries[0].Reference != "upload:bundle-mc" {
		t.Fatalf("entries = %+v", entries)
	}
	if !strings.HasPrefix(entries[0].Digest, "sha256:") {
		t.Errorf("digest = %q", entries[0].Digest)
	}

	b, err := f.Pull(context.Background(), "mc", "1.0.0")
	if err != nil || b.Metadata.Name != "mc" {
		t.Fatalf("Pull: %+v %v", b, err)
	}
	if _, err := f.Pull(context.Background(), "mc", "2.0.0"); err == nil ||
		!strings.Contains(err.Error(), "re-uploaded") {
		t.Errorf("stale version err = %v", err)
	}
	if _, err := f.Pull(context.Background(), "ghost", ""); err == nil {
		t.Error("missing module accepted")
	}
}

func TestUploadFetcher_StringDataAndWarnings(t *testing.T) {
	// kubectl apply with stringData lands in .data — both shapes index.
	stringCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bundle-string",
			Namespace: "gameplane-system",
			Labels:    map[string]string{gameplanev1alpha1.LabelModuleUpload: "true"},
		},
		Data: validModuleFiles("valheim", "0.9.0"),
	}
	broken := uploadCM("bundle-broken", map[string]string{FileMetadata: "name: broken\n"})
	dup := uploadCM("zz-bundle-dup", validModuleFiles("valheim", "2.0.0"))

	f := newUpload(uploadClient(stringCM, broken, dup), "gameplane-system", nil)
	entries, warnings, err := f.Index(context.Background())
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "valheim" || entries[0].LatestVersion != "0.9.0" {
		t.Fatalf("entries = %+v", entries)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "bundle-broken") || !strings.Contains(joined, "duplicate") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestUploadFetcher_AllowFilterAndEmpty(t *testing.T) {
	c := uploadClient(
		uploadCM("bundle-mc", validModuleFiles("mc", "1.0.0")),
		uploadCM("bundle-valheim", validModuleFiles("valheim", "1.0.0")),
	)
	f := newUpload(c, "gameplane-system", []string{"mc"})
	entries, _, err := f.Index(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Name != "mc" {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}

	// No uploads at all is a healthy empty catalog, not an error.
	empty := newUpload(uploadClient(), "gameplane-system", nil)
	entries, warnings, err := empty.Index(context.Background())
	if err != nil || len(entries) != 0 || len(warnings) != 0 {
		t.Fatalf("entries=%v warnings=%v err=%v", entries, warnings, err)
	}
}

func TestDigestFiles_MatchesContentDigest(t *testing.T) {
	files := validModuleFiles("mc", "1.0.0")
	fsys := moduleDirFS(map[string]map[string]string{"mc": files})
	fromFS, err := contentDigest(fsys, "mc")
	if err != nil {
		t.Fatalf("contentDigest: %v", err)
	}
	asBytes := map[string][]byte{}
	for k, v := range files {
		asBytes[k] = []byte(v)
	}
	if got := digestFiles(asBytes); got != fromFS {
		t.Errorf("digestFiles = %q, contentDigest = %q — same content must hash identically", got, fromFS)
	}
}

package handlers

import (
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

const (
	smtFeatureLabel = "gameplane.local/test-feature"
	smtNamespace    = "gp-system"
)

// smtClient builds a kube.Client backed by a fake clientset seeded with objs.
// Returning the concrete *kubefake.Clientset lets callers prepend reactors.
func smtClient(objs ...runtime.Object) (*kubefake.Clientset, *kube.Client) {
	cs := kubefake.NewClientset(objs...)
	return cs, &kube.Client{Typed: cs}
}

func smtSecret(name string, labels, data map[string]string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: smtNamespace, Labels: labels},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
}

func smtFailVerb(cs *kubefake.Clientset, verb string, injected error) {
	cs.PrependReactor(verb, "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, injected
	})
}

func TestUpsertLabelledSecret_Create(t *testing.T) {
	t.Parallel()

	_, k := smtClient()
	input := map[string]string{"a": "1", "b": "2"}
	if err := upsertLabelledSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel, input); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := k.Typed.CoreV1().Secrets(smtNamespace).Get(t.Context(), "creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Labels[smtFeatureLabel] != "true" || got.Labels[ManagedByLabel] != managedByValue {
		t.Fatalf("labels = %v", got.Labels)
	}
	if got.Type != corev1.SecretTypeOpaque {
		t.Fatalf("type = %q", got.Type)
	}
	if !reflect.DeepEqual(got.StringData, input) {
		t.Fatalf("stringData = %v, want %v", got.StringData, input)
	}
}

func TestUpsertLabelledSecret_UpdatesLabelledSecret(t *testing.T) {
	t.Parallel()

	existing := smtSecret("creds",
		map[string]string{smtFeatureLabel: "true", "extra": "keep-label"},
		map[string]string{"a": "old", "b": "keep"})
	existing.Data = map[string][]byte{"unrelated": []byte("keep-data")}
	_, k := smtClient(existing)

	if err := upsertLabelledSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel, map[string]string{"a": "new"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := k.Typed.CoreV1().Secrets(smtNamespace).Get(t.Context(), "creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Labels[ManagedByLabel] != managedByValue || got.Labels[smtFeatureLabel] != "true" {
		t.Fatalf("labels = %v", got.Labels)
	}
	if got.StringData["a"] != "new" {
		t.Fatalf("a = %q, want new", got.StringData["a"])
	}
	if got.StringData["b"] != "keep" {
		t.Fatalf("b = %q, want keep (credential merge must not drop it)", got.StringData["b"])
	}
	if string(got.Data["unrelated"]) != "keep-data" || got.Labels["extra"] != "keep-label" {
		t.Fatal("credential update dropped unrelated stored data or labels")
	}
}

func TestUpsertLabelledSecret_RefusesUnlabelledSecret(t *testing.T) {
	t.Parallel()

	existing := smtSecret("creds",
		map[string]string{"someone": "else"},
		map[string]string{"a": "orig"})
	_, k := smtClient(existing)
	before, err := k.Typed.CoreV1().Secrets(smtNamespace).Get(t.Context(), "creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	err = upsertLabelledSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel, map[string]string{"a": "new"})
	if !errors.Is(err, errNotManagedSecret) {
		t.Fatalf("err = %v, want errNotManagedSecret", err)
	}
	after, err := k.Typed.CoreV1().Secrets(smtNamespace).Get(t.Context(), "creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if !reflect.DeepEqual(before.Labels, after.Labels) {
		t.Fatalf("labels changed: before=%v after=%v", before.Labels, after.Labels)
	}
	if !reflect.DeepEqual(before.StringData, after.StringData) {
		t.Fatalf("data changed: before=%v after=%v", before.StringData, after.StringData)
	}
}

func TestUpsertLabelledSecret_ErrorPropagation(t *testing.T) {
	t.Parallel()

	injected := errors.New("injected")

	tests := []struct {
		name     string
		objs     []runtime.Object
		verb     string
		injected error
	}{
		{
			name:     "create error other than AlreadyExists is returned",
			objs:     nil,
			verb:     "create",
			injected: injected,
		},
		{
			name:     "get error after AlreadyExists is returned",
			objs:     []runtime.Object{smtSecret("creds", map[string]string{smtFeatureLabel: "true"}, nil)},
			verb:     "get",
			injected: injected,
		},
		{
			name:     "update error is returned",
			objs:     []runtime.Object{smtSecret("creds", map[string]string{smtFeatureLabel: "true"}, nil)},
			verb:     "update",
			injected: injected,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cs, k := smtClient(tc.objs...)
			smtFailVerb(cs, tc.verb, tc.injected)

			err := upsertLabelledSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel, map[string]string{"a": "new"})
			if !errors.Is(err, tc.injected) {
				t.Fatalf("err = %v, want injected", err)
			}
		})
	}
}

func TestDeleteManagedSecret(t *testing.T) {
	t.Parallel()

	managedLabels := func() map[string]string {
		return map[string]string{smtFeatureLabel: "true", ManagedByLabel: managedByValue}
	}

	t.Run("both labels deletes the secret", func(t *testing.T) {
		t.Parallel()

		_, k := smtClient(smtSecret("creds", managedLabels(), nil))
		if err := deleteManagedSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel); err != nil {
			t.Fatalf("delete: %v", err)
		}
		_, err := k.Typed.CoreV1().Secrets(smtNamespace).Get(t.Context(), "creds", metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("after delete, get err = %v, want NotFound", err)
		}
	})

	refusedCases := []struct {
		name   string
		labels map[string]string
	}{
		{
			name:   "feature label missing",
			labels: map[string]string{ManagedByLabel: managedByValue},
		},
		{
			name:   "managed-by label missing",
			labels: map[string]string{smtFeatureLabel: "true"},
		},
		{
			name:   "managed-by has another value",
			labels: map[string]string{smtFeatureLabel: "true", ManagedByLabel: "kubectl"},
		},
	}
	for _, tc := range refusedCases {
		t.Run(tc.name+" reads as not found and keeps the secret", func(t *testing.T) {
			t.Parallel()

			_, k := smtClient(smtSecret("creds", tc.labels, nil))
			err := deleteManagedSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel)
			if !apierrors.IsNotFound(err) {
				t.Fatalf("err = %v, want NotFound", err)
			}
			if _, err := k.Typed.CoreV1().Secrets(smtNamespace).Get(t.Context(), "creds", metav1.GetOptions{}); err != nil {
				t.Fatalf("secret should still exist: %v", err)
			}
		})
	}

	t.Run("get error is returned", func(t *testing.T) {
		t.Parallel()

		injected := errors.New("injected")
		cs, k := smtClient(smtSecret("creds", managedLabels(), nil))
		smtFailVerb(cs, "get", injected)
		err := deleteManagedSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel)
		if !errors.Is(err, injected) {
			t.Fatalf("err = %v, want injected", err)
		}
	})

	t.Run("secret not found at all", func(t *testing.T) {
		t.Parallel()

		_, k := smtClient()
		err := deleteManagedSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("err = %v, want NotFound", err)
		}
	})

	t.Run("delete error is returned", func(t *testing.T) {
		t.Parallel()

		injected := errors.New("injected")
		cs, k := smtClient(smtSecret("creds", managedLabels(), nil))
		smtFailVerb(cs, "delete", injected)
		err := deleteManagedSecret(t.Context(), k, smtNamespace, "creds", smtFeatureLabel)
		if !errors.Is(err, injected) {
			t.Fatalf("err = %v, want injected", err)
		}
	})
}

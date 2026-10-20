//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestDestinations_CreateGetListDelete(t *testing.T) {
	name := uniqueResourceName("dest")

	resp := doJSON(t, http.MethodPost, "/backup-destinations", map[string]string{
		"name":     name,
		"url":      "s3:s3.example.com/bucket",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d; body=%s", resp.StatusCode, readBody(t, resp))
	}
	var created destinationView
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	resp.Body.Close()
	if created.Name != name || created.URL != "s3:s3.example.com/bucket" || !created.HasPassword {
		t.Errorf("create projection wrong: %+v", created)
	}

	t.Cleanup(func() {
		_ = kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	// GET roundtrip
	resp = doJSON(t, http.MethodGet, "/backup-destinations/"+name, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d; body=%s", resp.StatusCode, readBody(t, resp))
	}
	var got destinationView
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got.Name != name || got.URL != "s3:s3.example.com/bucket" {
		t.Errorf("get projection wrong: %+v", got)
	}

	// LIST includes our destination
	resp = doJSON(t, http.MethodGet, "/backup-destinations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("LIST status = %d; body=%s", resp.StatusCode, readBody(t, resp))
	}
	var listed destinationListResp
	_ = json.NewDecoder(resp.Body).Decode(&listed)
	resp.Body.Close()
	found := false
	for _, d := range listed.Items {
		if d.Name == name {
			found = true
			if d.HasPassword == false {
				t.Errorf("listed entry %q has no password flag", name)
			}
		}
	}
	if !found {
		t.Errorf("listed destinations missing %q", name)
	}

	// DELETE returns 204
	resp = doJSON(t, http.MethodDelete, "/backup-destinations/"+name, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d; body=%s", resp.StatusCode, readBody(t, resp))
	}

	// Subsequent GET is 404
	resp = doJSON(t, http.MethodGet, "/backup-destinations/"+name, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("post-delete GET status = %d, want 404; body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// Plain GET on /backup-destinations must NOT return the password value
// in any field of the projection.
func TestDestinations_PasswordIsRedacted(t *testing.T) {
	name := uniqueResourceName("redact")
	const pw = "the-password-must-not-leak"

	resp := doJSON(t, http.MethodPost, "/backup-destinations", map[string]string{
		"name":     name,
		"url":      "s3:repo.example.com/x",
		"password": pw,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d; body=%s", resp.StatusCode, readBody(t, resp))
	}
	t.Cleanup(func() {
		_ = kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	resp.Body.Close()

	for _, path := range []string{"/backup-destinations", "/backup-destinations/" + name} {
		r := doJSON(t, http.MethodGet, path, nil)
		body := readBody(t, r)
		if strings.Contains(body, pw) {
			t.Errorf("response body for %s leaks password: %s", path, body)
		}
		_ = r.Body.Close()
	}
}

// A non-Gameplane Secret (no destinationLabel) is invisible to /backup-destinations.
// This protects against the route accidentally exposing arbitrary cluster secrets.
func TestDestinations_HidesUnlabeledSecrets(t *testing.T) {
	name := uniqueResourceName("foreign")
	_, err := kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).Create(
		context.Background(),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: scope.DefaultNamespace},
			StringData: map[string]string{"url": "s3:other", "password": "p"},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("seed unlabeled secret: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	// LIST must not include the foreign secret.
	resp := doJSON(t, http.MethodGet, "/backup-destinations", nil)
	body := readBody(t, resp)
	if strings.Contains(body, name) {
		t.Errorf("list exposed unlabeled secret %q: %s", name, body)
	}
	_ = resp.Body.Close()

	// GET must return 404 even though the Secret exists.
	resp = doJSON(t, http.MethodGet, "/backup-destinations/"+name, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unlabeled = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// DELETE must also refuse to nuke the foreign secret.
	resp = doJSON(t, http.MethodDelete, "/backup-destinations/"+name, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE unlabeled = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
	if _, err := kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).
		Get(context.Background(), name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		t.Errorf("foreign secret was deleted via /backup-destinations DELETE")
	}
}

// Repeating a POST for an existing destination updates the password
// in-place rather than failing with AlreadyExists.
func TestDestinations_RecreateIsRotation(t *testing.T) {
	name := uniqueResourceName("rotate")

	for i, pw := range []string{"first-password", "second-password"} {
		resp := doJSON(t, http.MethodPost, "/backup-destinations", map[string]string{
			"name":     name,
			"url":      "s3:s3.example.com/rot",
			"password": pw,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST iteration %d status = %d; body=%s", i, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
	t.Cleanup(func() {
		_ = kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	got, err := kubeC.Typed.CoreV1().Secrets(scope.DefaultNamespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get final secret: %v", err)
	}
	// After a JSON merge patch with stringData, the apiserver lifts the
	// new value into Data (base64). Read it back and compare.
	if string(got.Data["password"]) != "second-password" {
		t.Errorf("password not rotated: have %q, want %q",
			string(got.Data["password"]), "second-password")
	}
}

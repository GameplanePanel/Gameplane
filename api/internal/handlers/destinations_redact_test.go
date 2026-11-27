package handlers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRedactRepoURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"s3:s3.amazonaws.com/bucket", "s3:s3.amazonaws.com/bucket"},
		{"rest:https://host:8000/repo", "rest:https://host:8000/repo"},
		{"rest:https://alice:s3cret@host:8000/repo", "rest:https://alice:***@host:8000/repo"},
		{"rest:https://alice:pa/ss@host/repo", "rest:https://alice:***@host/repo"},
		{"rest:https://alice@host/repo", "rest:https://alice@host/repo"},
		{"sftp:user@host:/srv/restic", "sftp:user@host:/srv/restic"},
	}
	for _, c := range cases {
		if got := redactRepoURL(c.in); got != c.want {
			t.Errorf("redactRepoURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestProjectRedactsRepoPassword(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "dest"},
		Data: map[string][]byte{
			"repo":     []byte("rest:https://u:p@h/r"),
			"password": []byte("x"),
		},
	}
	got := project(s)
	if got.URL != "rest:https://u:***@h/r" {
		t.Errorf("URL = %q, want %q", got.URL, "rest:https://u:***@h/r")
	}
	if !got.HasPassword {
		t.Error("HasPassword = false, want true")
	}
}

package source

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// ocisrcClient is an in-process OCIClient that records its arguments and
// returns canned results.
type ocisrcClient struct {
	tags     []string
	tagsErr  error
	digest   string
	files    map[string][]byte
	pullErr  error
	gotRef   string
	gotTagOf string
}

func (c *ocisrcClient) ListTags(_ context.Context, ref string) ([]string, error) {
	c.gotRef = ref
	return c.tags, c.tagsErr
}

func (c *ocisrcClient) Pull(_ context.Context, ref, reference string) (string, map[string][]byte, error) {
	c.gotRef = ref
	c.gotTagOf = reference
	return c.digest, c.files, c.pullErr
}

func TestOCIRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, prefix, module, want string
	}{
		{"registry and name", "ghcr.io/org", "minecraft", "ghcr.io/org/minecraft"},
		{"trailing and leading slashes", "reg:5000/ns/", "/game", "reg:5000/ns/game"},
		{"empty prefix", "", "minecraft", "minecraft"},
		{"both empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := OCIRef(tc.prefix, tc.module); got != tc.want {
				t.Fatalf("OCIRef(%q, %q) = %q, want %q", tc.prefix, tc.module, got, tc.want)
			}
		})
	}
}

func TestOCIListVersions(t *testing.T) {
	t.Parallel()
	t.Run("returns tags unchanged", func(t *testing.T) {
		t.Parallel()
		cli := &ocisrcClient{tags: []string{"1.2.0", "1.10.0", "1.9.0"}}
		got, err := ListVersions(t.Context(), cli, "ghcr.io/org/minecraft")
		if err != nil {
			t.Fatalf("ListVersions: %v", err)
		}
		if cli.gotRef != "ghcr.io/org/minecraft" {
			t.Fatalf("ListTags ref = %q", cli.gotRef)
		}
		if !slices.Equal(got, []string{"1.2.0", "1.10.0", "1.9.0"}) {
			t.Fatalf("tags = %v", got)
		}
	})
	t.Run("wraps client failure", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("registry unreachable")
		cli := &ocisrcClient{tagsErr: boom}
		got, err := ListVersions(t.Context(), cli, "ghcr.io/org/minecraft")
		if !errors.Is(err, boom) {
			t.Fatalf("ListVersions error = %v, want wrapped sentinel", err)
		}
		if !strings.HasPrefix(err.Error(), "list tags: ") {
			t.Fatalf("error %q lacks list tags context", err)
		}
		if got != nil {
			t.Fatalf("tags = %v on failure, want nil", got)
		}
	})
}

func TestOCISortVersionsDesc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"single", []string{"1.0.0"}, []string{"1.0.0"}},
		{"numeric not lexical", []string{"1.2.0", "1.10.0", "1.9.0"}, []string{"1.10.0", "1.9.0", "1.2.0"}},
		{"already descending", []string{"2.0.0", "1.0.0"}, []string{"2.0.0", "1.0.0"}},
		{"prerelease sorts below release", []string{"1.0.0-rc.1", "1.0.0", "1.0.0-beta.2"}, []string{"1.0.0", "1.0.0-rc.1", "1.0.0-beta.2"}},
		{"invalid sorts last", []string{"0.9.0", "1.0.0", "junk"}, []string{"1.0.0", "0.9.0", "junk"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := slices.Clone(tc.in)
			SortVersionsDesc(in)
			if !slices.Equal(in, tc.want) {
				t.Fatalf("SortVersionsDesc(%v) = %v, want %v", tc.in, in, tc.want)
			}
		})
	}
}

func TestOCIPullFiles(t *testing.T) {
	t.Parallel()
	t.Run("pulls joined reference", func(t *testing.T) {
		t.Parallel()
		files := map[string][]byte{"module.yaml": []byte("name: mc\n")}
		cli := &ocisrcClient{digest: "sha256:abc", files: files}
		digest, got, err := PullFiles(t.Context(), cli, "ghcr.io/org", "minecraft", "1.2.0")
		if err != nil {
			t.Fatalf("PullFiles: %v", err)
		}
		if cli.gotRef != "ghcr.io/org/minecraft" || cli.gotTagOf != "1.2.0" {
			t.Fatalf("Pull called with ref=%q reference=%q", cli.gotRef, cli.gotTagOf)
		}
		if digest != "sha256:abc" {
			t.Fatalf("digest = %q", digest)
		}
		if string(got["module.yaml"]) != "name: mc\n" {
			t.Fatalf("files = %v", got)
		}
	})
	t.Run("propagates pull failure", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("manifest unknown")
		cli := &ocisrcClient{pullErr: boom}
		digest, files, err := PullFiles(t.Context(), cli, "ghcr.io/org", "minecraft", "9.9.9")
		if !errors.Is(err, boom) {
			t.Fatalf("PullFiles error = %v, want sentinel", err)
		}
		if digest != "" || files != nil {
			t.Fatalf("failed pull returned digest=%q files=%v", digest, files)
		}
	})
}

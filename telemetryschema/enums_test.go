package telemetryschema

import (
	"reflect"
	"strings"
	"testing"
)

// TestNodeBand covers every band boundary and the degenerate counts below 1.
func TestNodeBand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want string
	}{
		{-5, "1"}, {0, "1"}, {1, "1"},
		{2, "2-3"}, {3, "2-3"},
		{4, "4-10"}, {10, "4-10"},
		{11, "11-50"}, {50, "11-50"},
		{51, "51+"}, {52, "51+"}, {1000, "51+"},
	}
	for _, tc := range cases {
		if got := NodeBand(tc.n); got != tc.want {
			t.Errorf("NodeBand(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestClusterBand covers every band boundary and the degenerate counts below 1.
func TestClusterBand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		want string
	}{
		{-1, "1"}, {0, "1"}, {1, "1"},
		{2, "2-3"}, {3, "2-3"},
		{4, "4-10"}, {10, "4-10"},
		{11, "11+"}, {12, "11+"}, {500, "11+"},
	}
	for _, tc := range cases {
		if got := ClusterBand(tc.n); got != tc.want {
			t.Errorf("ClusterBand(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestBandsAreMembersOfTheirSets checks that every band function result is in
// the matching enumeration, so a folded value can never be a real band.
func TestBandsAreMembersOfTheirSets(t *testing.T) {
	t.Parallel()
	for n := -1; n <= 60; n++ {
		if got := NodeBand(n); SanitizeEnum(NodeBands, got) != got {
			t.Errorf("NodeBand(%d) = %q is not in NodeBands", n, got)
		}
		if got := ClusterBand(n); SanitizeEnum(ClusterBands, got) != got {
			t.Errorf("ClusterBand(%d) = %q is not in ClusterBands", n, got)
		}
	}
}

// TestSanitizeEnum checks membership, folding and exact matching.
func TestSanitizeEnum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		set  []string
		in   string
		want string
	}{
		{Distros, "k3s", "k3s"},
		{Distros, "talos", "talos"},
		{Distros, "other", "other"},
		{Distros, "K3S", "other"},
		{Distros, " k3s", "other"},
		{Distros, "", "other"},
		{Distros, "vanilla", "other"},
		{Arches, "riscv64", "riscv64"},
		{Arches, "x86_64", "other"},
		{Tunnels, "playit", "playit"},
		{Tunnels, "wireguard", "other"},
		{DBs, "postgres", "postgres"},
		{DBs, "mysql", "other"},
		{Languages, "en", "en"},
		{Languages, "fr", "other"},
		{nil, "anything", "other"},
		{[]string{}, "", "other"},
	}
	for _, tc := range cases {
		if got := SanitizeEnum(tc.set, tc.in); got != tc.want {
			t.Errorf("SanitizeEnum(%v, %q) = %q, want %q", tc.set, tc.in, got, tc.want)
		}
	}
}

// TestEnumerationContents pins the published category sets.
func TestEnumerationContents(t *testing.T) {
	t.Parallel()
	checks := []struct {
		name string
		got  []string
		want []string
	}{
		{"Distros", Distros, []string{"k3s", "rke2", "k0s", "eks", "gke", "aks", "openshift", "microk8s", "minikube", "kind", "doks", "talos", "other"}},
		{"Arches", Arches, []string{"amd64", "arm64", "arm", "ppc64le", "s390x", "riscv64", "other"}},
		{"Tunnels", Tunnels, []string{"frp", "tailscale", "playit", "other"}},
		{"DBs", DBs, []string{"sqlite", "postgres"}},
		{"Languages", Languages, []string{"en"}},
		{"NodeBands", NodeBands, []string{"1", "2-3", "4-10", "11-50", "51+"}},
		{"ClusterBands", ClusterBands, []string{"1", "2-3", "4-10", "11+"}},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if want := []float64{0, 1, 2, 5, 10, 25, 50, 100, 250}; !reflect.DeepEqual(FleetBands, want) {
		t.Errorf("FleetBands = %v, want %v", FleetBands, want)
	}
}

// TestVersionRE checks the syntax of a reported version label.
func TestVersionRE(t *testing.T) {
	t.Parallel()
	valid := []string{"0.3.0", "0.2.0-beta.8", "1", "v1.2.3", "1.0.0+build.5", "dev", "A", strings.Repeat("a", 32)}
	invalid := []string{"", ".1.0", "-1", "+1", "1 0", "<script>alert(1)</script>", "1/2", "1\n", strings.Repeat("a", 33), "ünï"}
	for _, v := range valid {
		if !VersionRE.MatchString(v) {
			t.Errorf("VersionRE rejects %q", v)
		}
	}
	for _, v := range invalid {
		if VersionRE.MatchString(v) {
			t.Errorf("VersionRE accepts %q", v)
		}
	}
}

// TestK8sMinorRE checks the 1.<minor> pattern.
func TestK8sMinorRE(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1.0", "1.9", "1.31", "1.123"} {
		if !K8sMinorRE.MatchString(v) {
			t.Errorf("K8sMinorRE rejects %q", v)
		}
	}
	for _, v := range []string{"", "1", "1.", "1.1234", "2.1", "v1.31", "1.31.2", "1.x", "01.31", "1.31\n"} {
		if K8sMinorRE.MatchString(v) {
			t.Errorf("K8sMinorRE accepts %q", v)
		}
	}
}

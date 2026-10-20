package telemetryschema

import (
	"regexp"
	"slices"
)

// other is the fold target for every out-of-set enumeration value.
const other = "other"

// VersionRE limits the syntax of a reported Gameplane version. The receiver
// buckets a version that does not match as "invalid".
var VersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$`)

// K8sMinorRE matches a Kubernetes version reported as 1.<minor>.
var K8sMinorRE = regexp.MustCompile(`^1\.[0-9]{1,3}$`)

// installIDRE matches a lowercase UUIDv4.
var installIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// The enumerations below are the fixed category sets of the report. A value
// outside its set is folded to "other" by SanitizeEnum. Treat them as
// read-only.
var (
	// Distros lists the recognised Kubernetes distributions.
	Distros = []string{"k3s", "rke2", "k0s", "eks", "gke", "aks", "openshift", "microk8s", "minikube", "kind", "doks", "talos", "other"}
	// Arches lists the recognised CPU architectures.
	Arches = []string{"amd64", "arm64", "arm", "ppc64le", "s390x", "riscv64", "other"}
	// Deployments lists how an install runs: on its own cluster, or as a
	// standalone panel with no local cluster (FR-045).
	Deployments = []string{"cluster", "standalone"}
	// Tunnels lists the recognised tunnel providers.
	Tunnels = []string{"frp", "tailscale", "playit", "other"}
	// DBs lists the recognised API database drivers.
	DBs = []string{"sqlite", "postgres"}
	// Languages lists the recognised dashboard languages.
	Languages = []string{"en"}
	// NodeBands lists the node-count bands returned by NodeBand.
	NodeBands = []string{"1", "2-3", "4-10", "11-50", "51+"}
	// ClusterBands lists the cluster-count bands returned by ClusterBand.
	ClusterBands = []string{"1", "2-3", "4-10", "11+"}
	// FleetBands are the histogram bucket upper bounds for server and
	// template counts; the +Inf bucket is implicit.
	FleetBands = []float64{0, 1, 2, 5, 10, 25, 50, 100, 250}
)

// SanitizeEnum returns v when it is a member of set and "other" otherwise.
func SanitizeEnum(set []string, v string) string {
	if slices.Contains(set, v) {
		return v
	}
	return other
}

// NodeBand maps a node count to its band: 1, 2-3, 4-10, 11-50 or 51+. A count
// below 1 is reported as "1".
func NodeBand(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 3:
		return "2-3"
	case n <= 10:
		return "4-10"
	case n <= 50:
		return "11-50"
	default:
		return "51+"
	}
}

// ClusterBand maps a cluster count (the local cluster plus registered
// clusters) to its band: 1, 2-3, 4-10 or 11+. A count below 1 is reported as
// "1".
func ClusterBand(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 3:
		return "2-3"
	case n <= 10:
		return "4-10"
	default:
		return "11+"
	}
}

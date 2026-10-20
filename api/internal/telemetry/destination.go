package telemetry

import (
	"fmt"
	"net/url"
	"strings"
)

// DefaultEndpoint is the project default telemetry destination, ruled in
// specs/done_022-default-telemetry-dashboard/OPEN-DECISIONS.md OD-1. It must stay
// an https URL (hack/check-telemetry-default.sh enforces this in CI).
const DefaultEndpoint = "https://telemetry.gameplane.net/ingest"

// Destination kinds, in the order ResolveDestination considers them.
const (
	// KindDisabled is a hard off set by the operator; nothing is ever sent.
	KindDisabled = "disabled"
	// KindBundled is the chart's in-cluster receiver.
	KindBundled = "bundled"
	// KindCustom is an operator-supplied endpoint.
	KindCustom = "custom"
	// KindDefault is the project default, DefaultEndpoint.
	KindDefault = "default"
	// KindNone means no destination is in effect, so nothing is sent.
	KindNone = "none"
)

// Destination is where reports go. URL is empty for the disabled and none
// kinds. Host is the URL's host (with any port), safe to show to admins.
type Destination struct {
	Kind string
	URL  string
	Host string
}

// ResolveDestination picks the one destination for this process from the
// operator's settings, first match wins: disabled, then bundled (an endpoint
// plus the chart's bundled marker), then custom (any endpoint), then the
// project default when DefaultEndpoint is set, otherwise none. The default
// must use https; any other scheme is an error so a typo can't send reports
// in clear text.
func ResolveDestination(disabled bool, endpoint string, bundled bool) (Destination, error) {
	return resolveDestination(disabled, endpoint, bundled, DefaultEndpoint)
}

// resolveDestination is ResolveDestination with the project default passed
// in, so the default branches can be exercised without editing the constant.
func resolveDestination(disabled bool, endpoint string, bundled bool, defaultEndpoint string) (Destination, error) {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case disabled:
		return Destination{Kind: KindDisabled}, nil
	case endpoint != "" && bundled:
		return Destination{Kind: KindBundled, URL: endpoint, Host: hostOf(endpoint)}, nil
	case endpoint != "":
		return Destination{Kind: KindCustom, URL: endpoint, Host: hostOf(endpoint)}, nil
	case defaultEndpoint != "":
		u, err := url.Parse(defaultEndpoint)
		if err != nil {
			return Destination{}, fmt.Errorf("default telemetry endpoint: %w", err)
		}
		if u.Scheme != "https" || u.Host == "" {
			return Destination{}, fmt.Errorf("default telemetry endpoint %q must be an https URL", defaultEndpoint)
		}
		return Destination{Kind: KindDefault, URL: defaultEndpoint, Host: u.Host}, nil
	default:
		return Destination{Kind: KindNone}, nil
	}
}

// hostOf returns the host of rawURL, or "" when it doesn't parse. Custom and
// bundled endpoints keep today's behaviour of accepting any value, so a bad
// one is not an error here.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

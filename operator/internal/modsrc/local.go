package modsrc

import (
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
)

// newLocal builds a Fetcher over a directory mounted into the operator
// pod. root comes from the --module-local-root flag (wired via Helm
// values together with the volume mount); sub is the source's relative
// spec.local.path under it.
func newLocal(root, sub string, allow []string) (Fetcher, error) {
	load, loc, err := source.LocalLoader(root, sub)
	if err != nil {
		return nil, err
	}
	return newFSFetcher(load, "local:"+loc, allow), nil
}

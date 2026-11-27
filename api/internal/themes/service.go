// Package themes runs the theme marketplace backend (spec 043): the admin
// theme sources, the indexer that caches their catalogs, and the per-user
// installs. It is one code path for combined and standalone modes; it needs
// the database and nothing from the cluster, because theme sources are stored
// in the panel database and their credentials are referenced by name only.
package themes

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/bundlefetch/verify"
)

// Deps are the collaborators a Service is built from.
type Deps struct {
	// Store holds theme sources, the catalog cache and installed themes.
	Store *db.Store
	// Logger receives operational messages. Nil uses slog.Default.
	Logger *slog.Logger
	// Clock returns the current time. Nil uses time.Now.
	Clock func() time.Time
	// LocalRoot confines local sources to one directory. Empty disables local
	// sources (indexing one then fails with a source error).
	LocalRoot string
	// OCI builds the registry client for an OCI source. Nil uses the bundlefetch
	// client with the theme artifact type and the package size cap. Tests swap
	// in an in-process fake.
	OCI func(insecure bool) source.OCIClient
	// Verifier builds the signature verifier for a source's verify JSON. Nil
	// uses bundlefetch/verify. Tests swap in a fake.
	Verifier func(ctx context.Context, verifyJSON string, insecure bool) (verify.Verifier, error)
}

// Service is the theme marketplace backend. Build it with New. Methods are
// safe for concurrent use: indexing state lives in the database, and a per
// source lock keeps two refreshes of one source from interleaving.
type Service struct {
	store       *db.Store
	logger      *slog.Logger
	clock       func() time.Time
	localRoot   string
	ociFor      func(insecure bool) source.OCIClient
	verifierFor func(ctx context.Context, verifyJSON string, insecure bool) (verify.Verifier, error)

	locksMu sync.Mutex
	locks   map[int64]*sync.Mutex

	verifiersMu sync.Mutex
	verifiers   map[string]verify.Verifier
}

// New returns a Service for the given dependencies.
func New(d Deps) *Service {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	ociFor := d.OCI
	if ociFor == nil {
		ociFor = defaultOCI
	}
	verifierFor := d.Verifier
	if verifierFor == nil {
		verifierFor = defaultVerifier
	}
	return &Service{
		store:       d.Store,
		logger:      logger,
		clock:       clock,
		localRoot:   d.LocalRoot,
		ociFor:      ociFor,
		verifierFor: verifierFor,
		locks:       map[int64]*sync.Mutex{},
		verifiers:   map[string]verify.Verifier{},
	}
}

// lockSource takes the refresh lock of one source and returns its unlock func.
func (s *Service) lockSource(id int64) func() {
	s.locksMu.Lock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	s.locksMu.Unlock()
	l.Lock()
	return l.Unlock
}

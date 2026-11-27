package themes

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/mod/semver"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/themepkg"
)

// MaxInstalledThemes is the number of themes one user may install (spec 043, T033).
const MaxInstalledThemes = 50

// Refusals of the install, update and remove operations. The handlers map
// them to the contract's status codes and messages; db.ErrThemeNotFound,
// db.ErrThemeAlreadyInstalled and db.ErrThemeInstallLimit come from the store.
var (
	// ErrEntryNotValid reports a catalog entry that is not valid, or whose
	// cached CSS no longer passes the sanitizer (contracts/themes-api.md, 422).
	ErrEntryNotValid = errors.New("catalog entry is not valid")
	// ErrSourceDisabled reports that the theme's source is disabled.
	ErrSourceDisabled = errors.New("theme source is disabled")
	// ErrSourceRemoved reports that the installed copy's source was deleted, or
	// that the installed copy has no source link any more.
	ErrSourceRemoved = errors.New("theme source was removed")
	// ErrNoUpdate reports that no newer valid version of the theme exists from
	// the same source.
	ErrNoUpdate = errors.New("no newer version available")
)

// Install copies the valid catalog entry entryID into userID's installed themes
// (data-model.md user_installed_themes). The CSS is sanitized again before it is
// stored. With selectTheme set, the user's preferences select the new theme in
// the same transaction. It returns db.ErrThemeNotFound for an unknown entry,
// ErrSourceDisabled, ErrEntryNotValid, db.ErrThemeAlreadyInstalled and
// db.ErrThemeInstallLimit.
func (s *Service) Install(ctx context.Context, userID, entryID int64, selectTheme bool) (db.InstalledTheme, error) {
	entry, err := s.store.GetThemeCatalogEntry(ctx, entryID)
	if err != nil {
		return db.InstalledTheme{}, err
	}
	src, err := s.store.GetThemeSourceByID(ctx, entry.SourceID)
	if err != nil {
		return db.InstalledTheme{}, err
	}
	if !src.Enabled {
		return db.InstalledTheme{}, ErrSourceDisabled
	}
	if entry.Status != "valid" {
		return db.InstalledTheme{}, ErrEntryNotValid
	}
	css, err := sanitizedCSS(entry.CSS)
	if err != nil {
		return db.InstalledTheme{}, err
	}
	sourceID := entry.SourceID
	t := db.InstalledTheme{
		UserID:     userID,
		SourceID:   &sourceID,
		SourceName: entry.SourceName,
		ThemeName:  entry.Name,
		Version:    entry.Version,
		Digest:     entry.Digest,
		Metadata:   entry.Metadata,
		Tokens:     entry.Tokens,
		CSS:        css,
	}
	return s.store.InstallThemeAndSelect(ctx, t, MaxInstalledThemes, selectTheme)
}

// Update moves the installed theme installedID of userID to the newer valid
// entry with the same source id and theme name. Matching uses the source id
// and never the name, so a recreated source never feeds old installs
// (data-model.md). It returns the installed theme before the update (for the
// audit target) and the one after it. It returns db.ErrThemeNotFound for a
// theme that is not the user's, and ErrSourceRemoved, ErrSourceDisabled and
// ErrNoUpdate for the refusals of the contract.
func (s *Service) Update(ctx context.Context, userID, installedID int64) (db.InstalledTheme, db.InstalledTheme, error) {
	cur, err := s.store.GetInstalledTheme(ctx, userID, installedID)
	if err != nil {
		return db.InstalledTheme{}, db.InstalledTheme{}, err
	}
	if cur.SourceID == nil {
		return cur, cur, ErrSourceRemoved
	}
	src, err := s.store.GetThemeSourceByID(ctx, *cur.SourceID)
	if errors.Is(err, db.ErrThemeNotFound) {
		return cur, cur, ErrSourceRemoved
	}
	if err != nil {
		return cur, cur, err
	}
	if !src.Enabled {
		return cur, cur, ErrSourceDisabled
	}
	entry, err := s.store.GetThemeCatalogEntryByName(ctx, *cur.SourceID, cur.ThemeName)
	if errors.Is(err, db.ErrThemeNotFound) {
		return cur, cur, ErrNoUpdate
	}
	if err != nil {
		return cur, cur, err
	}
	if entry.Status != "valid" || !semverNewer(entry.Version, cur.Version) {
		return cur, cur, ErrNoUpdate
	}
	css, err := sanitizedCSS(entry.CSS)
	if err != nil {
		return cur, cur, ErrNoUpdate
	}
	next := cur
	next.SourceName = entry.SourceName
	next.Version = entry.Version
	next.Digest = entry.Digest
	next.Metadata = entry.Metadata
	next.Tokens = entry.Tokens
	next.CSS = css
	if err := s.store.UpdateInstalledTheme(ctx, next); err != nil {
		return cur, cur, fmt.Errorf("update installed theme: %w", err)
	}
	return cur, next, nil
}

// Remove deletes the installed theme installedID of userID. A preference that
// selected it falls back to the Pink preset in the same transaction (FR-011,
// data-model.md). It returns the removed theme for the audit target.
func (s *Service) Remove(ctx context.Context, userID, installedID int64) (db.InstalledTheme, error) {
	cur, err := s.store.GetInstalledTheme(ctx, userID, installedID)
	if err != nil {
		return db.InstalledTheme{}, err
	}
	if err := s.store.DeleteInstalledTheme(ctx, userID, installedID); err != nil {
		return db.InstalledTheme{}, err
	}
	return cur, nil
}

// sanitizedCSS re-checks a stored overlay with the CSS sanitizer. A nil overlay
// stays nil; an overlay that fails the sanitizer makes the entry unusable.
func sanitizedCSS(css *string) (*string, error) {
	if css == nil {
		return nil, nil
	}
	if _, ok := themepkg.SanitizeCSS(*css); !ok {
		return nil, ErrEntryNotValid
	}
	return css, nil
}

// semverNewer reports whether version a is a higher release than b. Versions
// are the semver strings the indexer stores, without the leading "v".
func semverNewer(a, b string) bool {
	return semver.Compare("v"+a, "v"+b) > 0
}

// SemverNewer is the exported form of semverNewer, shared with the handlers
// that derive updateAvailable for the installed list.
func SemverNewer(a, b string) bool {
	return semverNewer(a, b)
}

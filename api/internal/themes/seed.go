package themes

import (
	"context"
	"errors"
	"fmt"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// OfficialSourceName is the reserved name of the seeded official theme source.
// Admins cannot create a source with this name (spec 043, data-model.md).
const OfficialSourceName = "official"

// OfficialSourceURL is the OCI repository the official theme source reads.
const OfficialSourceURL = "ghcr.io/gameplanepanel/gameplane-themes"

// OfficialKeylessIssuer is the OIDC issuer of the GitHub Actions workflow that
// signs official theme packages.
const OfficialKeylessIssuer = "https://token.actions.githubusercontent.com"

// OfficialKeylessIdentity is the exact Fulcio subject the official theme
// packages must carry. PLACEHOLDER: the release workflow of GameplanePanel/themes
// (T061) does not exist yet, and cosign matches the subject exactly, so this
// value must be replaced by the real workflow identity before the official
// source can verify any theme. Tracked in OPEN-DECISIONS.md (OD-6).
const OfficialKeylessIdentity = "https://github.com/GameplanePanel/themes/.github/workflows/release.yaml@refs/heads/master"

// officialRefreshIntervalS is the default refresh interval of the official
// source, in seconds (one hour).
const officialRefreshIntervalS = 3600

// officialSeededKey is the config key that records the official source was
// seeded once. Once set, an admin who deleted the source is not resurrected on
// the next start.
const officialSeededKey = "themes.official_seeded"

// SeedOfficial creates the reserved official theme source on the first start
// of a panel with enabled set (GAMEPLANE_THEMES_OFFICIAL_SOURCE, Helm
// themes.officialSource.enabled). It is insert-if-absent and runs at most once
// per database: after a successful run the seed marker is stored and later
// starts return without touching the sources. With enabled false it does
// nothing and leaves no marker, so enabling the source later still seeds it.
func (s *Service) SeedOfficial(ctx context.Context, enabled bool) error {
	if !enabled {
		return nil
	}
	_, seeded, err := s.store.ConfigValue(ctx, officialSeededKey)
	if err != nil {
		return fmt.Errorf("read theme seed marker: %w", err)
	}
	if seeded {
		return nil
	}
	_, err = s.store.CreateThemeSource(ctx, officialSourceRow())
	switch {
	case err == nil:
		s.logger.InfoContext(ctx, "theme source seeded", "source", OfficialSourceName)
	case errors.Is(err, db.ErrThemeSourceExists):
		// Another replica or an earlier run created it; keep that row.
	default:
		return fmt.Errorf("seed official theme source: %w", err)
	}
	if err := s.store.SetConfigValue(ctx, officialSeededKey, "true"); err != nil {
		return fmt.Errorf("write theme seed marker: %w", err)
	}
	return nil
}

// officialSourceRow is the row inserted by SeedOfficial. Spec and verify are
// JSON documents in the shape the indexer reads (data-model.md theme_sources).
// The themes list is empty until the official index is published (OD-6).
func officialSourceRow() db.ThemeSource {
	spec := `{"oci":{"url":"` + OfficialSourceURL + `","themes":[]}}`
	verify := `{"keyless":{"issuer":"` + OfficialKeylessIssuer + `","identity":"` + OfficialKeylessIdentity + `"}}`
	return db.ThemeSource{
		Name:             OfficialSourceName,
		Type:             "oci",
		Spec:             spec,
		Verify:           &verify,
		RefreshIntervalS: officialRefreshIntervalS,
		Enabled:          true,
	}
}

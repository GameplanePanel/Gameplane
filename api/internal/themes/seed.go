package themes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// OfficialSourceName is the reserved name of the seeded official theme source.
// Admins cannot create a source with this name (spec 043, data-model.md).
const OfficialSourceName = "official"

// OfficialSourceURL is the default OCI repository the official theme source
// reads. The chart sets the effective value (themes.officialSource.url).
const OfficialSourceURL = "ghcr.io/gameplanepanel/gameplane-themes"

// officialRefreshIntervalS is the default refresh interval of the official
// source, in seconds (one hour). An admin may change it; reconciliation keeps
// the stored value.
const officialRefreshIntervalS = 3600

// OfficialConfig is the chart configuration of the official theme source
// (themes.officialSource). The official signing key is the same key pair as the
// official modules (signing/cosign.pub), and the theme list ships in the chart
// the same way the module list does (OD-6, settled 2026-11-27).
type OfficialConfig struct {
	// Enabled seeds and reconciles the official source. False leaves any
	// existing row untouched and creates nothing.
	Enabled bool
	// URL is the OCI repository. Empty uses OfficialSourceURL.
	URL string
	// Themes is the list of theme names the official source indexes.
	Themes []string
	// VerifyEnabled sets a keyed cosign policy on the official source. When
	// false the source keeps no signature policy, as the chart default does.
	VerifyEnabled bool
	// CosignPublicKey is the PEM public key (signing/cosign.pub). Required when
	// VerifyEnabled is set.
	CosignPublicKey string
}

// SeedOfficial creates or reconciles the reserved official theme source from the
// chart configuration. It runs on every start of the panel:
//   - with cfg.Enabled false it does nothing;
//   - with no row it inserts the official source, enabled;
//   - with a row it rewrites the URL, the theme list and the verify policy from
//     cfg, so an upgrade that changes the list takes effect, and it keeps the
//     admin's enabled flag, refresh interval and credential.
//
// The official source cannot be deleted (409), so there is no seed marker: a
// missing row can only mean a fresh database.
func (s *Service) SeedOfficial(ctx context.Context, cfg OfficialConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.VerifyEnabled && strings.TrimSpace(cfg.CosignPublicKey) == "" {
		return errors.New("official theme source verify is enabled without a cosign public key")
	}
	desired, err := officialSourceRow(cfg)
	if err != nil {
		return err
	}

	current, err := s.store.GetThemeSource(ctx, OfficialSourceName)
	switch {
	case errors.Is(err, db.ErrThemeNotFound):
		_, err := s.store.CreateThemeSource(ctx, desired)
		switch {
		case err == nil:
			s.logger.InfoContext(ctx, "theme source seeded", "source", OfficialSourceName)
			return nil
		case errors.Is(err, db.ErrThemeSourceExists):
			// Another replica created it between the read and the insert; its
			// config is the same chart, so the next start reconciles any drift.
			return nil
		default:
			return fmt.Errorf("seed official theme source: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read official theme source: %w", err)
	}

	spec := mergeOfficialSpec(current.Spec, desired.Spec)
	if current.Spec == spec && equalStringPtr(current.Verify, desired.Verify) {
		return nil
	}
	next := current
	next.Spec = spec
	next.Verify = desired.Verify
	if err := s.store.UpdateThemeSource(ctx, next); err != nil {
		return fmt.Errorf("reconcile official theme source: %w", err)
	}
	s.logger.InfoContext(ctx, "theme source reconciled", "source", OfficialSourceName)
	return nil
}

// mergeOfficialSpec returns the stored spec with only the URL and the theme
// list taken from the chart, so admin settings on the row (allow globs, the
// insecure flag) survive a restart. A spec that does not decode as an oci spec
// is replaced by the desired one.
func mergeOfficialSpec(current, desired string) string {
	var sp sourceSpec
	if err := json.Unmarshal([]byte(current), &sp); err != nil || sp.OCI == nil {
		return desired
	}
	var want sourceSpec
	if err := json.Unmarshal([]byte(desired), &want); err != nil || want.OCI == nil {
		return desired
	}
	sp.OCI.URL = want.OCI.URL
	sp.OCI.Themes = want.OCI.Themes
	raw, err := json.Marshal(sp)
	if err != nil {
		return desired
	}
	if string(raw) == current {
		return current
	}
	return string(raw)
}

// officialSourceRow is the desired official row for cfg: the spec (url and
// themes) and the verify policy. Enabled and the refresh interval are the
// defaults for a new row; reconciliation does not overwrite them.
func officialSourceRow(cfg OfficialConfig) (db.ThemeSource, error) {
	url := cfg.URL
	if url == "" {
		url = OfficialSourceURL
	}
	themes := append([]string{}, cfg.Themes...)
	spec, err := json.Marshal(sourceSpec{OCI: &ociSpec{URL: url, Themes: themes}})
	if err != nil {
		return db.ThemeSource{}, fmt.Errorf("encode official theme source spec: %w", err)
	}
	var verify *string
	if cfg.VerifyEnabled {
		key := strings.TrimSpace(cfg.CosignPublicKey) + "\n"
		raw, err := json.Marshal(verifySpec{Key: &key})
		if err != nil {
			return db.ThemeSource{}, fmt.Errorf("encode official theme source verify: %w", err)
		}
		v := string(raw)
		verify = &v
	}
	return db.ThemeSource{
		Name:             OfficialSourceName,
		Type:             "oci",
		Spec:             string(spec),
		Verify:           verify,
		RefreshIntervalS: officialRefreshIntervalS,
		Enabled:          true,
	}, nil
}

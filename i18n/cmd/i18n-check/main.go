// Command i18n-check validates Gameplane's language packs.
//
// App mode (default) checks the reference pack and every translation under
// <locales-dir>. It can sync the reference's server.* and notify.* entries from a
// registry, check server.* and notify.* and extracted messages for drift, report unused ids, stamp
// source hashes, and write the language manifest and a markdown summary.
//
// Module mode (--module <template.yaml>) derives the reference from the
// template and the module.yaml beside it (package modlocale, the same ids as
// gp-module i18n extract and the operator), requires <locales-dir>/en.json to
// equal the derived set, and validates every other <tag>.json against it. File
// names must be valid language tags. Without a module.yaml the module.* ids are
// not derived, so an en.json that contains them reports unknown-id.
//
// Exit status: 0 when no error-severity finding was reported, 1 when one was,
// 2 for usage errors or unreadable required files.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GameplanePanel/gameplane/i18n/modlocale"
	"github.com/GameplanePanel/gameplane/i18n/pack"
	"gopkg.in/yaml.v3"
)

const (
	exitOK      = 0
	exitFinding = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options is the parsed command line.
type options struct {
	reference   string
	base        string
	extracted   string
	registry    string
	syncServer  bool
	checkServer bool
	usedIDs     string
	manifest    string
	stamp       bool
	module      string
	summary     string
	validOut    string
	dir         string
}

func run(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("i18n-check", flag.ContinueOnError)
	fset.SetOutput(stderr)
	var o options
	fset.StringVar(&o.reference, "reference", "en", "reference language tag")
	fset.StringVar(&o.base, "base", "", "previous language packs directory, for the summary's old completeness")
	fset.StringVar(&o.extracted, "extracted", "", "formatjs-extracted messages file to compare with the reference")
	fset.StringVar(&o.registry, "registry", "", "JSON registry of server.* and notify.* messages (required by --sync-server and --check-server)")
	fset.BoolVar(&o.syncServer, "sync-server", false, "replace the reference's server.* and notify.* entries with the registry")
	fset.BoolVar(&o.checkServer, "check-server", false, "report server.* and notify.* entries that differ from the registry")
	fset.StringVar(&o.usedIDs, "used-ids", "", "file listing the message ids code uses, one per line")
	fset.StringVar(&o.manifest, "manifest", "", "write the language manifest JSON to this file")
	fset.BoolVar(&o.stamp, "stamp", false, "record sourceHash on translated entries that lack one")
	fset.StringVar(&o.module, "module", "", "module mode: check flat <tag>.json packs against this template")
	fset.StringVar(&o.summary, "summary", "", "append a markdown summary to this file")
	fset.StringVar(&o.validOut, "valid-out", "", "write each pack's usable entries (the ones the dashboard shows) to <dir>/<tag>/app.json")

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fset.NArg() != 1 {
		say(stderr, "i18n-check: exactly one <locales-dir> argument is required\n")
		fset.Usage()
		return exitUsage
	}
	o.dir = fset.Arg(0)
	if err := o.checkUsage(); err != nil {
		return fail(stderr, err)
	}
	if o.module != "" {
		return runModule(o, stdout, stderr)
	}
	return runApp(o, stdout, stderr)
}

// checkUsage rejects flag combinations that cannot work together.
func (o options) checkUsage() error {
	if (o.syncServer || o.checkServer) && o.registry == "" {
		return errors.New("--sync-server and --check-server need --registry")
	}
	if o.module == "" {
		return nil
	}
	appOnly := []struct {
		name string
		set  bool
	}{
		{"--manifest", o.manifest != ""},
		{"--stamp", o.stamp},
		{"--sync-server", o.syncServer},
		{"--check-server", o.checkServer},
		{"--registry", o.registry != ""},
		{"--extracted", o.extracted != ""},
		{"--used-ids", o.usedIDs != ""},
		{"--base", o.base != ""},
		{"--valid-out", o.validOut != ""},
	}
	for _, f := range appOnly {
		if f.set {
			return fmt.Errorf("%s is not valid with --module", f.name)
		}
	}
	return nil
}

func runApp(o options, stdout, stderr io.Writer) int {
	refDir := filepath.Join(o.dir, o.reference)
	refFile := o.reference + "/app.json"
	refPath := filepath.Join(refDir, "app.json")
	refData, err := os.ReadFile(filepath.Clean(refPath))
	if err != nil {
		return fail(stderr, fmt.Errorf("read reference: %w", err))
	}

	var registry map[string]pack.Entry
	if o.syncServer || o.checkServer {
		if registry, err = loadRegistry(o.registry); err != nil {
			return fail(stderr, err)
		}
	}
	var extracted []byte
	if o.extracted != "" {
		if extracted, err = os.ReadFile(filepath.Clean(o.extracted)); err != nil {
			return fail(stderr, fmt.Errorf("read extracted messages: %w", err))
		}
	}
	var used map[string]struct{}
	if o.usedIDs != "" {
		if used, err = readIDs(o.usedIDs); err != nil {
			return fail(stderr, err)
		}
	}

	ref, err := pack.ParseEntries(refData)
	if err != nil {
		return report(stdout, []pack.Finding{errFinding(pack.CodeMalformedPack, refFile, pack.IDPack, err.Error())})
	}
	if o.syncServer {
		if err := syncServer(refPath, ref, registry); err != nil {
			return fail(stderr, err)
		}
		return exitOK
	}

	findings := pack.ValidateReference(ref, refFile)
	refMeta, _, metaFindings, err := readMeta(filepath.Join(refDir, "meta.json"), o.reference, o.reference+"/meta.json", false)
	if err != nil {
		return fail(stderr, err)
	}
	findings = append(findings, metaFindings...)
	if o.checkServer {
		findings = append(findings, checkServer(ref, registry, refFile)...)
	}
	if o.extracted != "" {
		findings = append(findings, checkExtracted(ref, extracted, refFile)...)
	}
	if o.usedIDs != "" {
		findings = append(findings, checkUsed(ref, used, refFile)...)
	}

	trs, trFindings, err := loadTranslations(o.dir, o.reference, ref)
	if err != nil {
		return fail(stderr, err)
	}
	findings = append(findings, trFindings...)
	if o.stamp {
		if err := stampTranslations(ref, trs); err != nil {
			return fail(stderr, err)
		}
	}

	if o.validOut != "" {
		if err := writeValidPacks(o.validOut, o.reference, ref, trs); err != nil {
			return fail(stderr, err)
		}
	}
	rows := manifestRows(o.reference, refMeta, ref, trs)
	if o.manifest != "" {
		if err := writeManifest(o.manifest, rows); err != nil {
			return fail(stderr, err)
		}
	}
	pack.SortFindings(findings)
	if o.summary != "" {
		if err := writeSummary(o.summary, findings, summaryRows(o, rows)); err != nil {
			return fail(stderr, err)
		}
	}
	return report(stdout, findings)
}

func runModule(o options, stdout, stderr io.Writer) int {
	tplData, err := os.ReadFile(filepath.Clean(o.module))
	if err != nil {
		return fail(stderr, fmt.Errorf("read template: %w", err))
	}
	derived, err := deriveModuleRefs(o.module, tplData)
	if err != nil {
		return fail(stderr, err)
	}

	refFile := o.reference + ".json"
	refData, err := os.ReadFile(filepath.Clean(filepath.Join(o.dir, refFile)))
	if err != nil {
		return fail(stderr, fmt.Errorf("read reference: %w", err))
	}
	ref, err := pack.ParseEntries(refData)
	if err != nil {
		return report(stdout, []pack.Finding{errFinding(pack.CodeMalformedPack, refFile, pack.IDPack, err.Error())})
	}
	findings := pack.ValidateReference(ref, refFile)
	// The reference must equal the ids derived from the YAML, even when that
	// set is empty; every translation is then validated against the derived set.
	findings = append(findings, checkModuleDrift(ref, derived, refFile)...)
	ref = derived

	dirEntries, err := os.ReadDir(o.dir)
	if err != nil {
		return fail(stderr, fmt.Errorf("read locales: %w", err))
	}
	for _, d := range dirEntries {
		name := d.Name()
		if d.IsDir() || name == refFile || !strings.HasSuffix(name, ".json") {
			continue
		}
		if stem := strings.TrimSuffix(name, ".json"); !pack.ValidTag(stem) {
			findings = append(findings, errFinding(pack.CodeMalformedPack, name, pack.IDPack, fmt.Sprintf("%q is not a valid language tag", stem)))
			continue
		}
		data, err := os.ReadFile(filepath.Clean(filepath.Join(o.dir, name)))
		if err != nil {
			return fail(stderr, fmt.Errorf("read %s: %w", name, err))
		}
		tr, err := pack.ParseEntries(data)
		if err != nil {
			findings = append(findings, errFinding(pack.CodeMalformedPack, name, pack.IDPack, err.Error()))
			continue
		}
		findings = append(findings, pack.ValidateTranslation(ref, tr, name)...)
	}

	pack.SortFindings(findings)
	if o.summary != "" {
		if err := writeSummary(o.summary, findings, nil); err != nil {
			return fail(stderr, err)
		}
	}
	return report(stdout, findings)
}

// deriveModuleRefs derives the reference pack from the template and the
// module.yaml that sits beside it (when there is one).
func deriveModuleRefs(templatePath string, templateData []byte) (map[string]pack.Entry, error) {
	var tmpl map[string]any
	if err := yaml.Unmarshal(templateData, &tmpl); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var meta map[string]any
	metaData, err := os.ReadFile(filepath.Clean(filepath.Join(filepath.Dir(templatePath), "module.yaml")))
	switch {
	case err == nil:
		if err := yaml.Unmarshal(metaData, &meta); err != nil {
			return nil, fmt.Errorf("parse module.yaml: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("read module.yaml: %w", err)
	}
	return modlocale.References(modlocale.Derive(meta, tmpl)), nil
}

// checkModuleDrift compares the written reference with the ids derived from
// the template: a derived id that is missing or whose English text changed is
// extract-drift, and an id the template no longer yields is unknown-id.
func checkModuleDrift(ref, derived map[string]pack.Entry, file string) []pack.Finding {
	var out []pack.Finding
	for _, id := range slices.Sorted(maps.Keys(derived)) {
		got, ok := ref[id]
		switch {
		case !ok:
			out = append(out, errFinding(pack.CodeExtractDrift, file, id, "template references this id but the reference pack does not have it"))
		case got.DefaultMessage != derived[id].DefaultMessage:
			out = append(out, errFinding(pack.CodeExtractDrift, file, id, "reference text differs from the template"))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(ref)) {
		if _, ok := derived[id]; !ok {
			out = append(out, errFinding(pack.CodeUnknownID, file, id, "reference has an id the template does not use"))
		}
	}
	return out
}

// translation is one non-reference app pack under the locales directory.
type translation struct {
	tag     string
	dir     string
	entries map[string]pack.Entry
	meta    pack.Meta
	metaOK  bool
}

// loadTranslations validates every other directory that has an app.json.
// Directories without one are skipped. Packs that fail to parse are reported
// and left out of the returned list.
func loadTranslations(dir, reference string, ref map[string]pack.Entry) ([]translation, []pack.Finding, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read locales: %w", err)
	}
	var (
		out      []translation
		findings []pack.Finding
	)
	for _, d := range dirEntries {
		if !d.IsDir() || d.Name() == reference {
			continue
		}
		tag := d.Name()
		packDir := filepath.Join(dir, tag)
		file := tag + "/app.json"
		data, err := os.ReadFile(filepath.Clean(filepath.Join(packDir, "app.json")))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", file, err)
		}
		meta, metaOK, metaFindings, err := readMeta(filepath.Join(packDir, "meta.json"), tag, tag+"/meta.json", true)
		if err != nil {
			return nil, nil, err
		}
		findings = append(findings, metaFindings...)
		entries, err := pack.ParseEntries(data)
		if err != nil {
			findings = append(findings, errFinding(pack.CodeMalformedPack, file, pack.IDPack, err.Error()))
			continue
		}
		findings = append(findings, pack.ValidateTranslation(ref, entries, file)...)
		out = append(out, translation{tag: tag, dir: packDir, entries: entries, meta: meta, metaOK: metaOK})
	}
	return out, findings, nil
}

// readMeta reads and validates a pack's meta.json. A missing file is a finding
// when required is set and otherwise means the pack has no metadata. Only an
// unreadable file is returned as an error; ok reports whether the metadata is
// usable.
func readMeta(path, tag, file string, required bool) (meta pack.Meta, ok bool, findings []pack.Finding, err error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			return pack.Meta{}, false, []pack.Finding{errFinding(pack.CodeInvalidMeta, file, pack.IDMeta, "meta.json is missing")}, nil
		}
		return pack.Meta{}, false, nil, nil
	}
	if err != nil {
		return pack.Meta{}, false, nil, fmt.Errorf("read %s: %w", file, err)
	}
	findings = pack.ValidateMeta(data, tag, file)
	meta, parseErr := pack.ParseMeta(data)
	return meta, parseErr == nil && !hasErrors(findings), findings, nil
}

// stampTranslations records the English source hash on every translated entry
// that lacks one, and rewrites the packs it changed.
func stampTranslations(ref map[string]pack.Entry, trs []translation) error {
	for i := range trs {
		tr := &trs[i]
		stamped, changed := pack.Stamp(ref, tr.entries)
		if !changed {
			continue
		}
		tr.entries = stamped
		data, err := pack.Marshal(tr.entries)
		if err != nil {
			return fmt.Errorf("stamp %s: %w", tr.tag, err)
		}
		if err := writeInDir(tr.dir, "app.json", data); err != nil {
			return fmt.Errorf("write stamped %s/app.json: %w", tr.tag, err)
		}
	}
	return nil
}

// writeInDir writes name inside dir through an os.Root, so the write cannot
// escape dir whatever the directory listing or flags contained.
func writeInDir(dir, name string, data []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile(name, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// writeValidPacks writes the pack the dashboard may show for every language to
// <dir>/<tag>/app.json. The reference pack is written whole. Other packs keep
// only the entries pack.Valid accepts, so invalid, unknown and missing ids are
// left out and the dashboard falls back to English for them.
func writeValidPacks(dir, reference string, ref map[string]pack.Entry, trs []translation) error {
	packs := map[string]map[string]pack.Entry{reference: ref}
	for _, tr := range trs {
		packs[tr.tag] = pack.Valid(ref, tr.entries)
	}
	for _, tag := range slices.Sorted(maps.Keys(packs)) {
		data, err := pack.Marshal(packs[tag])
		if err != nil {
			return fmt.Errorf("valid pack %s: %w", tag, err)
		}
		packDir := filepath.Join(dir, tag)
		if err := os.MkdirAll(packDir, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", packDir, err)
		}
		if err := writeInDir(packDir, "app.json", data); err != nil {
			return fmt.Errorf("write valid %s/app.json: %w", tag, err)
		}
	}
	return nil
}

// isRegistryID reports whether id belongs to the registry the API exports:
// API errors (server.*) and notification text (notify.*).
func isRegistryID(id string) bool {
	return strings.HasPrefix(id, "server.") || strings.HasPrefix(id, "notify.")
}

// syncServer replaces the reference's server.* and notify.* entries with the
// registry and writes the reference back.
func syncServer(path string, ref, registry map[string]pack.Entry) error {
	for id := range ref {
		if isRegistryID(id) {
			delete(ref, id)
		}
	}
	maps.Copy(ref, registry)
	data, err := pack.Marshal(ref)
	if err != nil {
		return err
	}
	if err := writeInDir(filepath.Dir(path), filepath.Base(path), data); err != nil {
		return fmt.Errorf("write reference: %w", err)
	}
	return nil
}

// checkServer reports server.* and notify.* ids that exist on only one side, or
// whose message or description differs between the reference and the registry.
func checkServer(ref, registry map[string]pack.Entry, file string) []pack.Finding {
	ids := map[string]struct{}{}
	for id := range ref {
		if isRegistryID(id) {
			ids[id] = struct{}{}
		}
	}
	for id := range registry {
		ids[id] = struct{}{}
	}
	var out []pack.Finding
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		r, inRef := ref[id]
		g, inReg := registry[id]
		switch {
		case !inRef:
			out = append(out, errFinding(pack.CodeServerDrift, file, id, "registry defines this server.* or notify.* message but the reference does not; run --sync-server"))
		case !inReg:
			out = append(out, errFinding(pack.CodeServerDrift, file, id, "reference has this server.* or notify.* message but the registry does not; run --sync-server"))
		case r.DefaultMessage != g.DefaultMessage || r.Description != g.Description:
			out = append(out, errFinding(pack.CodeServerDrift, file, id, "reference differs from the registry; run --sync-server"))
		}
	}
	return out
}

// isCodeID reports whether an id is extracted from source code. Server,
// condition and notification messages come from registries, not code.
func isCodeID(id string) bool {
	for _, p := range []string{"server.", "condition.", "notify."} {
		if strings.HasPrefix(id, p) {
			return false
		}
	}
	return true
}

// checkExtracted compares the reference with the formatjs extraction for the
// ids that come from code.
func checkExtracted(ref map[string]pack.Entry, data []byte, file string) []pack.Finding {
	ext, err := pack.ParseEntries(data)
	if err != nil {
		return []pack.Finding{errFinding(pack.CodeExtractDrift, file, pack.IDReference, "extracted messages do not parse: "+err.Error())}
	}
	ids := map[string]struct{}{}
	for id := range ref {
		if isCodeID(id) {
			ids[id] = struct{}{}
		}
	}
	for id := range ext {
		if isCodeID(id) {
			ids[id] = struct{}{}
		}
	}
	var out []pack.Finding
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		r, inRef := ref[id]
		e, inExt := ext[id]
		switch {
		case !inExt:
			out = append(out, errFinding(pack.CodeExtractDrift, file, id, "reference has this message but the code no longer extracts it"))
		case !inRef:
			out = append(out, errFinding(pack.CodeExtractDrift, file, id, "the code extracts this message but the reference does not have it"))
		case r.DefaultMessage != e.DefaultMessage || r.Description != e.Description:
			out = append(out, errFinding(pack.CodeExtractDrift, file, id, "reference text differs from the message extracted from the code"))
		}
	}
	return out
}

// checkUsed reports reference ids that no code uses.
func checkUsed(ref map[string]pack.Entry, used map[string]struct{}, file string) []pack.Finding {
	var out []pack.Finding
	for _, id := range slices.Sorted(maps.Keys(ref)) {
		if _, ok := used[id]; !ok {
			out = append(out, errFinding(pack.CodeUnusedID, file, id, "not used by any code; delete it from the reference"))
		}
	}
	return out
}

// registryItem is one entry of the --registry JSON array.
type registryItem struct {
	ID          string `json:"id"`
	English     string `json:"english"`
	Description string `json:"description"`
}

func loadRegistry(path string) (map[string]pack.Entry, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var items []registryItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", path, err)
	}
	out := make(map[string]pack.Entry, len(items))
	for _, it := range items {
		out[it.ID] = pack.Entry{DefaultMessage: it.English, Description: it.Description}
	}
	return out, nil
}

// readIDs reads one message id per line, ignoring blank lines.
func readIDs(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read used ids: %w", err)
	}
	set := map[string]struct{}{}
	for _, line := range strings.Split(string(data), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			set[id] = struct{}{}
		}
	}
	return set, nil
}

// manifestRow is one language in the --manifest JSON.
type manifestRow struct {
	Tag          string `json:"tag"`
	NativeName   string `json:"nativeName"`
	EnglishName  string `json:"englishName"`
	Direction    string `json:"direction"`
	Completeness int    `json:"completeness"`
	Listed       bool   `json:"listed"`
}

func newRow(tag string, m pack.Meta, completeness float64, listed bool) manifestRow {
	return manifestRow{
		Tag:          tag,
		NativeName:   m.NativeName,
		EnglishName:  m.EnglishName,
		Direction:    m.Direction,
		Completeness: int(math.Floor(completeness)),
		Listed:       listed,
	}
}

// manifestRows lists the reference first, then the translations with valid
// metadata in tag order.
func manifestRows(reference string, refMeta pack.Meta, ref map[string]pack.Entry, trs []translation) []manifestRow {
	rows := []manifestRow{newRow(reference, refMeta, 100, true)}
	for _, tr := range trs {
		if !tr.metaOK {
			continue
		}
		c := pack.Completeness(ref, tr.entries)
		rows = append(rows, newRow(tr.tag, tr.meta, c, pack.Listed(tr.tag, tr.meta, c)))
	}
	return rows
}

func writeManifest(path string, rows []manifestRow) error {
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	// The manifest lands in a generated directory (web/src/generated/i18n,
	// or /out in the image build) that may not exist yet.
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(path)), 0o750); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// summaryRow is one translation row of the markdown summary.
type summaryRow struct {
	tag          string
	completeness string
	listed       bool
}

// summaryRows renders completeness as "old → new" when --base is given and the
// tag exists there, "—" → new when it does not, and plain "new" otherwise.
func summaryRows(o options, rows []manifestRow) []summaryRow {
	var out []summaryRow
	for _, r := range rows {
		if r.Tag == o.reference {
			continue
		}
		cur := fmt.Sprintf("%d%%", r.Completeness)
		if o.base != "" {
			prev := "—"
			if c, ok := baseCompleteness(o.base, o.reference, r.Tag); ok {
				prev = fmt.Sprintf("%d%%", int(math.Floor(c)))
			}
			cur = prev + " → " + cur
		}
		out = append(out, summaryRow{tag: r.Tag, completeness: cur, listed: r.Listed})
	}
	return out
}

// baseCompleteness returns the completeness of tag's pack in the base
// directory, measured against the base reference.
func baseCompleteness(base, reference, tag string) (float64, bool) {
	ref, err := readEntriesFile(filepath.Join(base, reference, "app.json"))
	if err != nil {
		return 0, false
	}
	tr, err := readEntriesFile(filepath.Join(base, tag, "app.json"))
	if err != nil {
		return 0, false
	}
	return pack.Completeness(ref, tr), true
}

func readEntriesFile(path string) (map[string]pack.Entry, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	entries, err := pack.ParseEntries(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return entries, nil
}

// writeSummary appends a markdown section to path: a heading, the error and
// notice counts, an optional language table, and the findings in a code block.
func writeSummary(path string, findings []pack.Finding, rows []summaryRow) error {
	errs, notices := countSeverities(findings)
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "### i18n-check\n\n%d error(s), %d notice(s)\n\n", errs, notices)
	if len(rows) > 0 {
		b.WriteString("| Language | Completeness | Listed |\n| --- | --- | --- |\n")
		for _, r := range rows {
			listed := "no"
			if r.listed {
				listed = "yes"
			}
			_, _ = fmt.Fprintf(&b, "| %s | %s | %s |\n", r.tag, r.completeness, listed)
		}
		b.WriteString("\n")
	}
	if len(findings) > 0 {
		b.WriteString("```text\n")
		for _, f := range findings {
			b.WriteString(f.String() + "\n")
		}
		b.WriteString("```\n")
	}
	out, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open summary: %w", err)
	}
	if _, err := out.WriteString(b.String()); err != nil {
		_ = out.Close()
		return fmt.Errorf("write summary: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close summary: %w", err)
	}
	return nil
}

// report prints the findings sorted and returns the exit status.
func report(stdout io.Writer, findings []pack.Finding) int {
	pack.SortFindings(findings)
	code := exitOK
	for _, f := range findings {
		say(stdout, "%s\n", f.String())
		if f.Severity == pack.SeverityError {
			code = exitFinding
		}
	}
	return code
}

func errFinding(code, file, id, msg string) pack.Finding {
	return pack.Finding{Code: code, Severity: pack.SeverityError, File: file, ID: id, Message: msg}
}

func hasErrors(findings []pack.Finding) bool {
	errs, _ := countSeverities(findings)
	return errs > 0
}

func countSeverities(findings []pack.Finding) (errs, notices int) {
	for _, f := range findings {
		if f.Severity == pack.SeverityError {
			errs++
		} else {
			notices++
		}
	}
	return errs, notices
}

func fail(stderr io.Writer, err error) int {
	say(stderr, "i18n-check: %v\n", err)
	return exitUsage
}

func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

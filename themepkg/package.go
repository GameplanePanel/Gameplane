package themepkg

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Package limits from specs/043-theme-marketplace/contracts/theme-package.md.
const (
	MaxPackageBytes  = 1536 * 1024
	MaxPreviewBytes  = 512 * 1024
	MaxPreviewWidth  = 1600
	MaxPreviewHeight = 1000
)

// Preview is one validated preview image of a theme package.
type Preview struct {
	Mode        string `json:"mode"`
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Data        []byte `json:"-"`
}

// Result is the outcome of Validate. Manifest is set whenever theme.yaml was
// present and decoded, CSS only when theme.css passed every CSS check, and
// Previews holds the valid preview of each mode that has tokens.
type Result struct {
	Manifest    Manifest
	CSS         string
	Previews    map[string]Preview
	LowContrast bool
}

// allowedPackageFiles is the closed set of paths a theme package may contain.
var allowedPackageFiles = map[string]bool{
	"theme.yaml":          true,
	"theme.css":           true,
	"previews/light.png":  true,
	"previews/light.webp": true,
	"previews/dark.png":   true,
	"previews/dark.webp":  true,
}

// Validate checks a whole theme package, given as file path to content, and
// reports every problem it finds in a deterministic order. The result is
// returned even when problems exist.
func Validate(files map[string][]byte) (Result, []Problem) {
	var problems []Problem
	res := Result{Previews: map[string]Preview{}}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	problems = append(problems, checkPackageSize(files)...)
	problems = append(problems, checkPackageFiles(paths)...)

	data, hasManifest := files["theme.yaml"]
	if !hasManifest {
		problems = append(problems, Problem{Rule: RuleSchema, Detail: "theme.yaml is missing"})
	} else {
		m, probs := DecodeManifest(data)
		res.Manifest = m
		problems = append(problems, probs...)
	}

	if css, ok := files["theme.css"]; ok {
		problems = append(problems, checkCSS(string(css), &res)...)
	}

	if hasManifest {
		var probs []Problem
		probs, res.LowContrast = checkPreviews(files, paths, res.Manifest, res.Previews)
		problems = append(problems, probs...)
	}

	return res, problems
}

// checkPackageSize reports a package whose files together exceed MaxPackageBytes.
func checkPackageSize(files map[string][]byte) []Problem {
	total := 0
	for _, data := range files {
		total += len(data)
	}
	if total <= MaxPackageBytes {
		return nil
	}
	return []Problem{{Rule: RuleSizeTotal, Detail: fmt.Sprintf("package is %d bytes; the limit is 1.5 MiB", total)}}
}

// checkPackageFiles reports every path outside the allowed set, and both a PNG
// and a WebP preview for the same mode. paths must be sorted.
func checkPackageFiles(paths []string) []Problem {
	var problems []Problem
	for _, p := range paths {
		if !allowedPackageFiles[p] {
			problems = append(problems, Problem{Rule: RuleFilesExtra, Detail: "unexpected file " + p})
		}
	}
	for _, mode := range []string{"light", "dark"} {
		if contains(paths, "previews/"+mode+".png") && contains(paths, "previews/"+mode+".webp") {
			problems = append(problems, Problem{Rule: RuleFilesExtra, Detail: fmt.Sprintf("previews/%s has both a .png and a .webp; keep one", mode)})
		}
	}
	return problems
}

// checkCSS runs SanitizeCSS and CheckCSSComments, both reported when they fail,
// and stores the CSS in res only when both pass.
func checkCSS(css string, res *Result) []Problem {
	var problems []Problem
	sanitizeOK := true
	if p, ok := SanitizeCSS(css); !ok {
		problems = append(problems, p)
		sanitizeOK = false
	}
	commentsOK := true
	if p, ok := CheckCSSComments(css); !ok {
		problems = append(problems, p)
		commentsOK = false
	}
	if sanitizeOK && commentsOK {
		res.CSS = css
	}
	return problems
}

// checkPreviews validates the preview of every mode with tokens, stores the
// valid ones in previews, reports previews for modes without tokens, and
// reports whether any mode with valid tokens has low contrast.
func checkPreviews(files map[string][]byte, paths []string, m Manifest, previews map[string]Preview) ([]Problem, bool) {
	var problems []Problem
	lowContrast := false
	modes := m.Modes()
	inManifest := map[string]bool{}
	for _, mode := range modes {
		inManifest[mode] = true
	}

	for _, mode := range modes {
		tokens := modeTokens(m, mode)
		if len(ValidateTokens(mode, tokens)) == 0 && LowContrast(tokens) {
			lowContrast = true
		}

		p, ok := previewPath(paths, mode)
		if !ok {
			problems = append(problems, Problem{Rule: RulePreviewMissing, Detail: fmt.Sprintf("previews/%s.png or .webp is missing", mode)})
			continue
		}
		if prev, probs := checkPreview(mode, p, files[p]); len(probs) > 0 {
			problems = append(problems, probs...)
		} else {
			previews[mode] = prev
		}
	}

	for _, p := range paths {
		mode, ok := previewMode(p)
		if !ok || inManifest[mode] {
			continue
		}
		problems = append(problems, Problem{Rule: RuleFilesExtra, Detail: fmt.Sprintf("%s has no tokens.%s in theme.yaml", p, mode)})
	}
	return problems, lowContrast
}

// previewPath returns the preview file of mode, PNG or WebP, when present.
func previewPath(paths []string, mode string) (string, bool) {
	for _, ext := range []string{".png", ".webp"} {
		if p := "previews/" + mode + ext; contains(paths, p) {
			return p, true
		}
	}
	return "", false
}

// previewMode returns the mode a preview path belongs to, for allowed preview paths.
func previewMode(p string) (string, bool) {
	if !strings.HasPrefix(p, "previews/") {
		return "", false
	}
	base := path.Base(p)
	for _, mode := range []string{"light", "dark"} {
		if base == mode+".png" || base == mode+".webp" {
			return mode, true
		}
	}
	return "", false
}

// checkPreview checks one preview's size, header format, extension and
// dimensions. It returns the Preview and no problems when all pass.
func checkPreview(mode, p string, data []byte) (Preview, []Problem) {
	var problems []Problem
	if len(data) > MaxPreviewBytes {
		problems = append(problems, Problem{Rule: RulePreviewFormat, Detail: fmt.Sprintf("%s is %d bytes; the limit is 512 KiB", p, len(data))})
	}
	ct, w, h, err := PreviewInfo(data)
	if err != nil {
		problems = append(problems, Problem{Rule: RulePreviewFormat, Detail: fmt.Sprintf("%s is not a readable PNG or WebP image (%v)", p, err)})
		return Preview{}, problems
	}
	if want := contentTypeForPath(p); want != ct {
		problems = append(problems, Problem{Rule: RulePreviewFormat, Detail: fmt.Sprintf("%s holds %s but its extension says %s", p, ct, want)})
	}
	if w <= 0 || h <= 0 || w > MaxPreviewWidth || h > MaxPreviewHeight {
		problems = append(problems, Problem{Rule: RulePreviewFormat, Detail: fmt.Sprintf("%s is %dx%d pixels; the limit is %dx%d", p, w, h, MaxPreviewWidth, MaxPreviewHeight)})
	}
	if len(problems) > 0 {
		return Preview{}, problems
	}
	return Preview{Mode: mode, Path: p, ContentType: ct, Width: w, Height: h, Data: data}, nil
}

// contentTypeForPath maps a preview extension to its media type.
func contentTypeForPath(p string) string {
	if strings.HasSuffix(p, ".webp") {
		return "image/webp"
	}
	return "image/png"
}

// contains reports whether the sorted or unsorted slice holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

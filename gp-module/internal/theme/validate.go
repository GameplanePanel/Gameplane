package theme

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// Package is a theme folder read from disk and checked with themepkg.Validate.
// Files maps each slash-separated relative path to its content; Result and
// Problems are exactly what themepkg reports.
type Package struct {
	Dir      string
	Files    map[string][]byte
	Result   themepkg.Result
	Problems []themepkg.Problem
}

// Valid reports whether the package passed every rule. Warnings (lowContrast)
// do not make a package invalid.
func (p *Package) Valid() bool {
	return len(p.Problems) == 0
}

// Inspect reads every file under dir and validates the package. Only I/O
// failures are returned as an error; rule failures are in Problems.
func Inspect(dir string) (*Package, error) {
	files, err := Load(dir)
	if err != nil {
		return nil, err
	}
	res, probs := themepkg.Validate(files)
	return &Package{Dir: dir, Files: files, Result: res, Problems: probs}, nil
}

// Load reads every regular file under dir into a map keyed by the relative,
// slash-separated path. Symbolic links are refused: a package is what its folder
// holds, and a link could pull in files from outside it.
func Load(dir string) (map[string][]byte, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("theme folder: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", dir)
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		// Dotfiles and dot-folders (.git, .DS_Store, editor swap files) are
		// not part of a package, so they are skipped without being read.
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link not allowed in a theme package: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", path, err)
		}
		data, err := readCapped(path, rel)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// readCapped reads one package file, refusing to hold more than the package
// size limit in memory. A larger file cannot be part of a valid package.
func readCapped(path, rel string) ([]byte, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, themepkg.MaxPackageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	if len(data) > themepkg.MaxPackageBytes {
		return nil, fmt.Errorf("%s is larger than the %d-byte package limit", rel, themepkg.MaxPackageBytes)
	}
	return data, nil
}

// Warning is a non-blocking finding. Only lowContrast is raised today.
type Warning struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// Report is the machine-readable result of `theme validate --json`.
type Report struct {
	Dir      string             `json:"dir"`
	Valid    bool               `json:"valid"`
	Name     string             `json:"name,omitempty"`
	Version  string             `json:"version,omitempty"`
	Modes    []string           `json:"modes,omitempty"`
	Problems []themepkg.Problem `json:"problems"`
	Warnings []Warning          `json:"warnings"`
}

// Report builds the machine-readable summary of a checked package.
func (p *Package) Report() Report {
	r := Report{
		Dir:      p.Dir,
		Valid:    p.Valid(),
		Name:     p.Result.Manifest.Name,
		Version:  p.Result.Manifest.Version,
		Modes:    p.Result.Manifest.Modes(),
		Problems: append([]themepkg.Problem{}, p.Problems...),
		Warnings: []Warning{},
	}
	if p.Result.LowContrast {
		r.Warnings = append(r.Warnings, Warning{
			Rule:   "lowContrast",
			Detail: "a text or accent pair is below the contrast floor (4.5:1 text, 3:1 accent on surface) in a mode; the package is still valid",
		})
	}
	return r
}

// JSON encodes the report with two-space indentation.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Human formats the report for a terminal.
func (r Report) Human() string {
	var b strings.Builder
	if r.Name != "" {
		_, _ = fmt.Fprintf(&b, "theme %s %s (modes: %s)\n", r.Name, r.Version, strings.Join(r.Modes, ", "))
	}
	for _, p := range r.Problems {
		_, _ = fmt.Fprintf(&b, "error %s\n", p.Error())
	}
	for _, w := range r.Warnings {
		_, _ = fmt.Fprintf(&b, "warning %s: %s\n", w.Rule, w.Detail)
	}
	if r.Valid {
		_, _ = fmt.Fprintf(&b, "%s: valid\n", r.Dir)
	} else {
		_, _ = fmt.Fprintf(&b, "%s: invalid (%d problem(s))\n", r.Dir, len(r.Problems))
	}
	return b.String()
}

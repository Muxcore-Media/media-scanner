package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/module/pathguard"
)

// baseRootsFromConfig returns the operator-configured directories every
// caller-supplied watch/library path must resolve inside (RULE-VAL-1): the
// start-up library, TV and music roots, the default watch directory and
// SCANNER_ALLOWED_ROOTS / Config.AllowedRoots. Only absolute paths count.
// These come from the process environment, never from UpdateSetting, so a
// settings change cannot widen its own allow-list.
func baseRootsFromConfig(cfg Config) []string {
	cands := []string{cfg.LibraryRoot, cfg.TVLibraryRoot, cfg.MusicLibraryRoot, cfg.DefaultWatchDir}
	cands = append(cands, cfg.AllowedRoots...)
	if v := os.Getenv("SCANNER_ALLOWED_ROOTS"); v != "" {
		cands = append(cands, filepath.SplitList(v)...)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, c := range cands {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !filepath.IsAbs(c) {
			slog.Warn("media-scanner: ignoring non-absolute allowed root", "root", c)
			continue
		}
		c = filepath.Clean(c)
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// allowedRoots returns the configured base roots plus the accessible roots
// registered in media-root-folders. When media-root-folders cannot be reached
// only the configured roots are used (narrower, never wider).
func (m *Module) allowedRoots(ctx context.Context) []string {
	roots := append([]string(nil), m.baseRoots...)
	if regs, ok := m.listRegisteredRoots(ctx); ok {
		for _, r := range regs {
			p := strings.TrimSpace(r.GetPath())
			if p == "" || !filepath.IsAbs(p) || !r.GetAccessible() {
				continue
			}
			roots = append(roots, filepath.Clean(p))
		}
	}
	return roots
}

// confineConfiguredPath checks a caller-supplied watch or library path. An
// empty value is accepted (it means "unset / keep default").
func (m *Module) confineConfiguredPath(ctx context.Context, field, p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	roots := m.allowedRoots(ctx)
	if len(roots) == 0 {
		return fmt.Errorf("%s %q refused: no allowed roots configured (SCANNER_ALLOWED_ROOTS)", field, p)
	}
	if _, err := pathguard.Confine(p, roots); err != nil {
		return fmt.Errorf("%s %q refused: %w (allowed roots: %s)", field, p, err, strings.Join(roots, ", "))
	}
	return nil
}

// confinedWatchDir returns the deepest watch directory that path resolves
// inside. Unlike a lexical prefix test it resolves symlinks (a link inside a
// watch directory that points elsewhere does not count) and compares by path
// component (/downloads2 is not inside /downloads).
func confinedWatchDir(dirs []watchDirEntry, path string) (watchDirEntry, bool) {
	var best watchDirEntry
	bestLen := -1
	for _, d := range dirs {
		root := filepath.Clean(d.path)
		if !filepath.IsAbs(root) {
			continue
		}
		if _, err := pathguard.Confine(path, []string{root}); err != nil {
			continue
		}
		if len(root) > bestLen {
			best = d
			bestLen = len(root)
		}
	}
	return best, bestLen >= 0
}

// warnWatchDirsOutsideRoots logs stored watch directories created before
// path confinement that fall outside the configured roots.
func (m *Module) warnWatchDirsOutsideRoots() {
	if len(m.baseRoots) == 0 {
		return
	}
	for _, d := range m.collectWatchDirs() {
		for field, p := range map[string]string{"path": d.path, "library_path": d.libPath, "tv_library_path": d.tvLibPath} {
			if strings.TrimSpace(p) == "" {
				continue
			}
			if _, err := pathguard.Confine(p, m.baseRoots); err != nil {
				slog.Warn("media-scanner: stored watch dir outside configured roots (not under SCANNER_* roots; allowed only if registered in media-root-folders)", "field", field, "value", p)
			}
		}
	}
}

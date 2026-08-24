package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func formatWatchRoots(dirs []watchDirEntry) string {
	if len(dirs) == 0 {
		return "none"
	}
	roots := make([]string, 0, len(dirs))
	seen := map[string]struct{}{}
	for _, d := range dirs {
		root := filepath.Clean(d.path)
		if root == "" || root == "." {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		return "none"
	}
	return strings.Join(roots, ", ")
}

func importPathZeroImportedError(path string, found, imported, skipped int) error {
	if imported > 0 || found == 0 {
		return nil
	}
	if skipped > 0 {
		return fmt.Errorf("found %d media file(s) under %q but imported 0 (%d skipped)", found, path, skipped)
	}
	return fmt.Errorf("found %d media file(s) under %q but imported 0", found, path)
}

func importPathWatchDirError(path, unresolvedRel string, dirs []watchDirEntry) error {
	roots := formatWatchRoots(dirs)
	if unresolvedRel != "" {
		return fmt.Errorf("relative import path %q could not be resolved under watch directories (%s)", unresolvedRel, roots)
	}
	return fmt.Errorf("path %q is not under any registered watch directory (%s)", path, roots)
}

func importPathSingleFileError(path string) error {
	return fmt.Errorf("failed to import %q: unrecognized, rejected, or blocked media file", path)
}

func importPathCheckCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

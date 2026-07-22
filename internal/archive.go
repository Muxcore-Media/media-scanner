package internal

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nwaples/rardecode/v2"
)

const archiveStabilityAge = 2 * time.Second

var (
	rePartRar = regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)
	reOldVol  = regexp.MustCompile(`(?i)\.r\d{2}$`)
)

func archiveKind(name string) (kind string, isFirst bool) {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".zip") {
		return "zip", true
	}
	if m := rePartRar.FindStringSubmatch(lower); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return "rar", n == 1
	}
	if reOldVol.MatchString(lower) {
		return "rar", false
	}
	if strings.HasSuffix(lower, ".rar") {
		return "rar", true
	}
	return "", false
}

func isArchiveStable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) < archiveStabilityAge {
		return false
	}
	return true
}

func extractDirFor(archivePath string) string {
	base := filepath.Base(archivePath)
	lower := strings.ToLower(base)
	if m := rePartRar.FindStringSubmatch(lower); len(m) == 2 {
		base = base[:len(base)-len(m[0])]
		return filepath.Join(filepath.Dir(archivePath), base)
	}
	ext := filepath.Ext(base)
	return filepath.Join(filepath.Dir(archivePath), strings.TrimSuffix(base, ext))
}

func safeExtractPath(destRoot, name string) (string, error) {
	cleaned := filepath.Clean(filepath.Join(destRoot, filepath.FromSlash(name)))
	rel, err := filepath.Rel(filepath.Clean(destRoot), cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("zip slip rejected: %s", name)
	}
	return cleaned, nil
}

func (m *Module) maybeExtractArchive(archivePath string) (extractedDir string, err error) {
	kind, isFirst := archiveKind(filepath.Base(archivePath))
	if kind == "" || !isFirst {
		return "", nil
	}
	if !isArchiveStable(archivePath) {
		return "", nil
	}

	dest := extractDirFor(archivePath)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}

	var volumes []string
	switch kind {
	case "zip":
		volumes, err = extractZip(archivePath, dest)
	case "rar":
		volumes, err = extractRar(archivePath, dest)
	default:
		return "", nil
	}
	if err != nil {
		return "", err
	}

	for _, v := range volumes {
		if rmErr := os.Remove(v); rmErr != nil {
			slog.Warn("failed to remove archive volume", "path", v, "error", rmErr)
		}
	}
	slog.Info("extracted archive", "archive", archivePath, "dest", dest, "volumes", len(volumes))
	return dest, nil
}

func extractZip(archivePath, dest string) ([]string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		target, err := safeExtractPath(dest, f.Name)
		if err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return nil, err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return []string{archivePath}, nil
}

func extractRar(archivePath, dest string) ([]string, error) {
	rc, err := rardecode.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	for {
		hdr, err := rc.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Encrypted || hdr.HeaderEncrypted {
			return nil, fmt.Errorf("password-protected rar not supported: %s", archivePath)
		}
		target, err := safeExtractPath(dest, hdr.Name)
		if err != nil {
			return nil, err
		}
		if hdr.IsDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}

	vols := rc.Volumes()
	if len(vols) == 0 {
		vols = []string{archivePath}
	}
	return vols, nil
}

package internal

import (
	"os"
	"path/filepath"
	"testing"

	renamev1 "github.com/Muxcore-Media/media-rename/proto/renamev1"
)

func TestPreviewRelPath(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "lib", "Movies")
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"absolute inside root", filepath.Join(base, "Fight Club (1999)", "Fight Club.mkv"), filepath.Join("Fight Club (1999)", "Fight Club.mkv"), true},
		{"absolute outside root", "/home/u/downloads/rel/Fight Club (1999)/a.mkv", "", false},
		{"absolute sibling prefix", "/lib/Movies2/x.mkv", "", false},
		{"absolute is root", base, "", false},
		{"relative", "Fight Club (1999)/a.mkv", filepath.Join("Fight Club (1999)", "a.mkv"), true},
		{"relative dotdot", "../../etc/passwd", "", false},
		{"relative nested dotdot escape", "a/../../b.mkv", "", false},
		{"relative dotdot staying inside", "a/../b.mkv", "b.mkv", true},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		got, ok := previewRelPath(base, c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: previewRelPath(%q) = %q,%v want %q,%v", c.name, c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestImportAbsolutePreviewPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(tmp, lib string) string
		want func(lib string) string
	}{
		{"inside root", func(_, lib string) string {
			return filepath.Join(lib, "Movies", "Fight Club (1999)", "Fight Club (1999).mkv")
		}, func(lib string) string {
			return filepath.Join(lib, "Movies", "Fight Club (1999)", "Fight Club (1999).mkv")
		}},
		{"under source folder falls back", func(tmp, _ string) string {
			return filepath.Join(tmp, "downloads", "rel", "Fight Club (1999)", "Fight Club (1999).mkv")
		}, func(lib string) string {
			return filepath.Join(lib, "Movies", "Fight Club (1999)", "Fight Club.1999.1080p.BluRay.mkv")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModule(t)
			tmp := t.TempDir()
			src := filepath.Join(tmp, "Fight.Club.1999.1080p.BluRay.mkv")
			if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			lib := filepath.Join(tmp, "library")
			m.previewRenameFn = func(*renamev1.PreviewRequest) (*renamev1.PreviewResponse, error) {
				return &renamev1.PreviewResponse{NewPath: tc.path(tmp, lib)}, nil
			}
			if !m.importFile(src, filepath.Base(src), "movie", lib, "") {
				t.Fatal("import failed")
			}
			if _, err := os.Stat(tc.want(lib)); err != nil {
				t.Fatalf("missing %s: %v", tc.want(lib), err)
			}
			if _, err := os.Stat(filepath.Join(lib, "Movies", tmp)); err == nil {
				t.Fatal("absolute path was nested under library root")
			}
		})
	}
}

func TestImportRelativePreviewAppliesTemplate(t *testing.T) {
	m := newTestModule(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "downloads", "rel", "Fight.Club.1999.1080p.BluRay.mkv")
	if err := os.MkdirAll(filepath.Dir(src), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(tmp, "library")
	// Mirrors resolveLibraryDest: a relative request path yields the
	// templated path unchanged; an absolute one yields an absolute path.
	m.previewRenameFn = func(req *renamev1.PreviewRequest) (*renamev1.PreviewResponse, error) {
		templated := filepath.Join("Fight Club (1999)", "Fight Club (1999) [1080p].mkv")
		if filepath.IsAbs(req.GetFilePath()) {
			return &renamev1.PreviewResponse{NewPath: filepath.Join(filepath.Dir(req.GetFilePath()), templated)}, nil
		}
		return &renamev1.PreviewResponse{NewPath: templated}, nil
	}
	if !m.importFile(src, filepath.Base(src), "movie", lib, "") {
		t.Fatal("import failed")
	}
	want := filepath.Join(lib, "Movies", "Fight Club (1999)", "Fight Club (1999) [1080p].mkv")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("missing templated dest %s: %v", want, err)
	}
}

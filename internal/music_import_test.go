package internal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsAudioExt(t *testing.T) {
	for _, ext := range []string{".mp3", ".flac", ".m4a", ".ogg"} {
		if !isAudioExt(ext) {
			t.Fatalf("expected audio ext %s", ext)
		}
	}
	if isAudioExt(".mkv") {
		t.Fatal("mkv is not audio")
	}
}

func TestParseMusicFileArtistAlbumTrack(t *testing.T) {
	p := parseMusicFile("Radiohead - OK Computer - 01 Airbag.flac", "", "/music")
	if p.Artist != "Radiohead" || p.Album != "OK Computer" || p.Title != "Airbag" {
		t.Fatalf("unexpected parse: %+v", p)
	}
	if p.MediaType != "music" {
		t.Fatalf("expected music type, got %q", p.MediaType)
	}
}

func TestParseMusicFileFromPath(t *testing.T) {
	root := t.TempDir()
	artistDir := filepath.Join(root, "Daft Punk")
	albumDir := filepath.Join(artistDir, "Discovery")
	if err := os.MkdirAll(albumDir, 0755); err != nil {
		t.Fatal(err)
	}
	track := filepath.Join(albumDir, "01 One More Time.flac")
	if err := os.WriteFile(track, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	p := parseMusicFile("01 One More Time.flac", track, root)
	if p.Artist != "Daft Punk" || p.Album != "Discovery" || p.Title != "One More Time" {
		t.Fatalf("unexpected path parse: %+v", p)
	}
}

func TestImportMusicFile(t *testing.T) {
	tmp := t.TempDir()
	downloads := filepath.Join(tmp, "downloads")
	musicLib := filepath.Join(tmp, "library", "music")
	if err := os.MkdirAll(downloads, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(musicLib, 0755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(downloads, "Bjork - Post - 01 Army of Me.flac")
	if err := os.WriteFile(src, []byte("fake-flac"), 0600); err != nil {
		t.Fatal(err)
	}

	m := NewModule(Config{
		DBPath:           filepath.Join(tmp, "scanner.db"),
		GRPCAddr:         ":0",
		LibraryRoot:      filepath.Join(tmp, "library", "movies"),
		MusicLibraryRoot: musicLib,
		ImportMode:       "copy",
		MinVideoBytes:    0,
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })

	if !m.importMusicFile(src, filepath.Base(src), "music", m.libraryRoot) {
		t.Fatal("importMusicFile returned false")
	}

	dest := filepath.Join(musicLib, "Bjork", "Post", "Army of Me.flac")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("expected dest %s: %v", dest, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("source should remain after copy mode")
	}
}

func TestBuildMusicDestPath(t *testing.T) {
	p := parsedFile{
		FileName:  "01 Track.flac",
		MediaType: "music",
		Artist:    "Artist Name",
		Album:     "Album Name",
		Title:     "Track",
	}
	dest := buildMusicDestPath(p, "/data/library/music")
	if !strings.Contains(dest, "Artist Name") || !strings.Contains(dest, "Album Name") {
		t.Fatalf("unexpected dest: %s", dest)
	}
}

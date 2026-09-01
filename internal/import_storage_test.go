package internal

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
)

type fakeMeshStorage struct {
	objects map[string][]byte
}

func (f *fakeMeshStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeMeshStorage) List(_ context.Context, prefix string) ([]*storagev1.StatResponse, error) {
	var out []*storagev1.StatResponse
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, &storagev1.StatResponse{Key: k})
		}
	}
	return out, nil
}

func TestImportStorageObjectMovie(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "library")
	m.libraryRoot = lib
	m.storageTestOverride = &fakeMeshStorage{
		objects: map[string][]byte{
			"torrent/abc/files/Fight.Club.1999.1080p.mkv": []byte("fake-movie-bytes"),
		},
	}

	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{
		Path: "storage://torrent/abc/files/Fight.Club.1999.1080p.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetFilesImported() != 1 {
		t.Fatalf("expected 1 imported, got %+v", resp)
	}
	dest := filepath.Join(lib, "Movies", "Fight Club (1999)", "Fight Club.1999.1080p.mkv")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("expected local dest %s: %v", dest, err)
	}
	if !m.isAlreadyImported("storage://torrent/abc/files/Fight.Club.1999.1080p.mkv") {
		t.Fatal("storage URI should be recorded as imported")
	}
}

func TestImportStorageObjectSkipsSampleName(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()
	m.storageTestOverride = &fakeMeshStorage{
		objects: map[string][]byte{
			"torrent/x/files/sample.trailer.mkv": []byte("tiny-sample"),
		},
	}
	resp, err := m.ImportPath(ctx, &scannerv1.ImportPathRequest{Path: "storage://torrent/x/files/sample.trailer.mkv"})
	if err == nil {
		t.Fatalf("expected zero-import error, got %+v", resp)
	}
}

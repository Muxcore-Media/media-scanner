package internal

import "testing"

func TestSidecarDestName(t *testing.T) {
	got := sidecarDestName("Movie.2010.1080p", "Movie.en", "Movie.2010.1080p", ".srt")
	want := "Movie.2010.1080p.en.srt"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = sidecarDestName("Inception.2010.1080p", "Inception.2010.1080p.en", "Inception.2010.1080p", ".srt")
	want = "Inception.2010.1080p.en.srt"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSidecarStemMatchesScanner(t *testing.T) {
	if !sidecarStemMatches("Movie.en", "Movie.2010.1080p") {
		t.Fatal("expected torrent-style sidecar to match")
	}
}

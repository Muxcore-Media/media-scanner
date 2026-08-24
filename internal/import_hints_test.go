package internal

import "testing"

func TestApplyImportHints(t *testing.T) {
	parsed := parsedFile{Title: "paddington bear 1989", Season: 1, Episode: 1, MediaType: "tv"}
	hints := &ImportHints{TmdbID: 40792, Title: "Paddington Bear", Year: 1989, SeasonNumber: 1, EpisodeNumber: 1}
	applyImportHints(&parsed, hints)
	if parsed.TMDBID != 40792 {
		t.Fatalf("tmdb=%d", parsed.TMDBID)
	}
	if parsed.Title != "Paddington Bear" {
		t.Fatalf("title=%q", parsed.Title)
	}
	if parsed.Year != 1989 {
		t.Fatalf("year=%d", parsed.Year)
	}
}

func TestImportHintsFromRequestEmpty(t *testing.T) {
	if importHintsFromRequest(nil) != nil {
		t.Fatal("expected nil")
	}
}

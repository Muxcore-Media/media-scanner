package internal

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Muxcore-Media/contracts-media/events"
)

func (m *Module) publishFileImported(ctx context.Context, p events.FileImportedPayload) {
	if m.mc == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := json.Marshal(p)
	if err != nil {
		slog.Warn("marshal file imported event", "error", err)
		return
	}
	if err := m.mc.Events.Publish(ctx, events.EventFileImported, m.id, data); err != nil {
		slog.Warn("publish event failed", "type", events.EventFileImported, "error", err)
	}
}

func (m *Module) publishImportFailed(ctx context.Context, path, errMsg string) {
	if m.mc == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p := events.ImportFailedPayload{Path: path, Error: errMsg}
	data, err := json.Marshal(p)
	if err != nil {
		slog.Warn("marshal import failed event", "error", err)
		return
	}
	if err := m.mc.Events.Publish(ctx, events.EventImportFailed, m.id, data); err != nil {
		slog.Warn("publish event failed", "type", events.EventImportFailed, "error", err)
	}
}

func (m *Module) failImport(ctx context.Context, originalPath, reason string) {
	m.recordFailed(originalPath, reason)
	go m.publishImportFailed(context.Background(), originalPath, reason)
}

func fileImportedPayloadFromParsed(originalPath, destPath, storageKey, quality string, parsed parsedFile) events.FileImportedPayload {
	p := events.FileImportedPayload{
		OriginalPath:    originalPath,
		DestinationPath: destPath,
		StorageKey:      storageKey,
		MediaType:       parsed.MediaType,
		Title:           parsed.Title,
		Year:            int32(parsed.Year),
		SeasonNumber:    int32(parsed.Season),
		EpisodeNumber:   int32(parsed.Episode),
		EpisodeNumbers:  episodeNumbersInt32(parsed.Episodes, parsed.Episode),
		AbsoluteNumber:  int32(parsed.AbsoluteNumber),
		AirDate:         parsed.AirDate,
		SeasonPack:      parsed.SeasonPack,
		Quality:         quality,
		TMDBID:          int32(parsed.TMDBID),
	}
	if parsed.MediaType == "music" {
		p.Artist = parsed.Artist
		p.Album = parsed.Album
		p.TrackTitle = parsed.Title
		if parsed.Artist != "" && parsed.Title != "" {
			p.Title = parsed.Artist + " - " + parsed.Title
		}
	}
	return p
}

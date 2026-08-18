package internal

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (m *Module) startWatcher() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.watcher = w
	m.watched = make(map[string]struct{})
	m.mu.Unlock()
	return m.syncWatches()
}

func (m *Module) stopWatcher() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
	m.debounceMu.Lock()
	if m.debounceTimer != nil {
		m.debounceTimer.Stop()
		m.debounceTimer = nil
	}
	m.debounceMu.Unlock()

	m.mu.Lock()
	w := m.watcher
	m.watcher = nil
	m.watched = nil
	m.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

func (m *Module) syncWatches() error {
	m.mu.RLock()
	w := m.watcher
	m.mu.RUnlock()
	if w == nil {
		return nil
	}

	dirs := m.collectWatchDirs()
	want := make(map[string]struct{})
	for _, d := range dirs {
		_ = filepath.WalkDir(d.path, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				want[path] = struct{}{}
			}
			return nil
		})
		want[d.path] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watcher == nil {
		return nil
	}
	for path := range m.watched {
		if _, ok := want[path]; !ok {
			_ = m.watcher.Remove(path)
			delete(m.watched, path)
		}
	}
	for path := range want {
		if _, ok := m.watched[path]; ok {
			continue
		}
		if err := m.watcher.Add(path); err != nil {
			slog.Warn("fsnotify add watch failed", "path", path, "error", err)
			continue
		}
		m.watched[path] = struct{}{}
	}
	return nil
}

func (m *Module) watchLoop(ctx context.Context) {
	if m.initialDelay > 0 {
		time.Sleep(m.initialDelay)
	}
	m.scheduleScan()
	go m.safetyLoop(ctx)

	m.mu.RLock()
	w := m.watcher
	m.mu.RUnlock()
	if w == nil {
		<-ctx.Done()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			m.handleFSEvent(ev)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Warn("fsnotify error", "error", err)
		}
	}
}

func (m *Module) safetyLoop(ctx context.Context) {
	for {
		d := m.getSafetyRescan()
		if d <= 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				continue
			}
		}
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			m.scheduleScan()
		}
	}
}

func (m *Module) handleFSEvent(ev fsnotify.Event) {
	if ev.Has(fsnotify.Create) {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			m.mu.Lock()
			if m.watcher != nil {
				if err := m.watcher.Add(ev.Name); err == nil {
					if m.watched == nil {
						m.watched = make(map[string]struct{})
					}
					m.watched[ev.Name] = struct{}{}
				}
			}
			m.mu.Unlock()
		}
	}
	if !m.eventUnderWatch(ev.Name) {
		return
	}
	if ignoreIncompleteWatchPath(ev.Name) {
		return
	}
	if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Write) || ev.Has(fsnotify.Rename) || ev.Has(fsnotify.Remove) {
		m.scheduleScan()
	}
}

// ignoreIncompleteWatchPath skips in-progress torrent pieces so WRITE storms
// on *.part files do not rescan the whole downloads tree every few seconds.
func ignoreIncompleteWatchPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(base, ".part") || strings.HasSuffix(base, ".!ut") || strings.HasSuffix(base, ".tmp") {
		return true
	}
	return false
}

func (m *Module) eventUnderWatch(path string) bool {
	dirs := m.collectWatchDirs()
	clean := filepath.Clean(path)
	for _, d := range dirs {
		root := filepath.Clean(d.path)
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			if d.libPath != "" {
				lib := filepath.Clean(d.libPath)
				if clean == lib || strings.HasPrefix(clean, lib+string(os.PathSeparator)) {
					if !strings.HasPrefix(lib, root+string(os.PathSeparator)) && lib != root {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

func (m *Module) scheduleScan() {
	m.debounceMu.Lock()
	defer m.debounceMu.Unlock()
	if m.debounceTimer != nil {
		m.debounceTimer.Stop()
	}
	wait := m.debounceWait
	if wait <= 0 {
		wait = 400 * time.Millisecond
	}
	m.debounceTimer = time.AfterFunc(wait, func() {
		m.runScan()
	})
}

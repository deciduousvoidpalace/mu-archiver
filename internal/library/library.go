// Package library maintains the on-disk manifest of archived episodes
// (<out>/.mu-archiver/library.json). The manifest is what makes "skip what I
// already have" reliable: feeds routinely report an inaccurate <enclosure
// length>, so size comparison against the feed alone would re-download files
// forever. A file is complete when the manifest says so and the file on disk
// still has the recorded size. Files written by older versions of the tool
// (or copied in by hand) are adopted on first sight.
package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mu-dl/internal/feed"
	"mu-dl/internal/util"
)

// Entry records one archived episode.
type Entry struct {
	Key        string    `json:"key"`
	Path       string    `json:"path"` // relative to the output dir
	Size       int64     `json:"size"`
	Show       string    `json:"show"`
	Season     int       `json:"season"`
	Episode    int       `json:"episode"`
	Title      string    `json:"title"`
	Published  time.Time `json:"published,omitempty"`
	Downloaded time.Time `json:"downloaded"`
	Source     string    `json:"source,omitempty"` // enclosure URL
}

// Library is safe for concurrent use.
type Library struct {
	mu      sync.Mutex
	root    string
	layout  feed.Layout
	entries map[string]Entry
	dirty   bool
}

const (
	dirName  = ".mu-archiver"
	fileName = "library.json"
)

// Open loads (or creates) the manifest under root. layout decides where
// files live; files recorded under another layout are moved on first sight.
func Open(root string, layout feed.Layout) (*Library, error) {
	l := &Library{root: root, layout: layout, entries: map[string]Entry{}}
	data, err := os.ReadFile(l.path())
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	var doc struct {
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		// a corrupt manifest must not brick the archive: start over and let
		// adoption rebuild it from the files on disk
		return l, nil
	}
	for _, e := range doc.Entries {
		l.entries[e.Key] = e
	}
	return l, nil
}

// Root returns the output directory.
func (l *Library) Root() string { return l.root }

// Layout returns the file layout in use.
func (l *Library) Layout() feed.Layout { return l.layout }

// PathFor returns the canonical relative path of an episode.
func (l *Library) PathFor(e feed.Episode) string { return e.PathIn(l.layout) }

func (l *Library) path() string { return filepath.Join(l.root, dirName, fileName) }

// Save writes the manifest if it changed (atomic temp+rename).
func (l *Library) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.dirty {
		return nil
	}
	return l.saveLocked()
}

func (l *Library) saveLocked() error {
	entries := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	data, err := json.MarshalIndent(struct {
		Version int     `json:"version"`
		Entries []Entry `json:"entries"`
	}{1, entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path()), 0o755); err != nil {
		return err
	}
	tmp := l.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.path()); err != nil {
		return err
	}
	l.dirty = false
	return nil
}

// Get returns the entry for an episode.
func (l *Library) Get(e feed.Episode) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	en, ok := l.entries[e.Key()]
	return en, ok
}

// Count returns the number of archived entries.
func (l *Library) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// Entries returns a copy of all entries.
func (l *Library) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// TotalBytes sums the recorded sizes.
func (l *Library) TotalBytes() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n int64
	for _, e := range l.entries {
		n += e.Size
	}
	return n
}

// Record marks an episode as archived at rel with the given size.
func (l *Library) Record(e feed.Episode, rel string, size int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[e.Key()] = Entry{
		Key: e.Key(), Path: filepath.ToSlash(rel), Size: size,
		Show: e.Show, Season: e.Season, Episode: e.Episode, Title: e.Title,
		Published: e.Published, Downloaded: time.Now(), Source: e.EnclosureURL,
	}
	l.dirty = true
}

// Forget removes an episode from the manifest (e.g. before --overwrite).
func (l *Library) Forget(e feed.Episode) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[e.Key()]; ok {
		delete(l.entries, e.Key())
		l.dirty = true
	}
}

// Status describes what the library knows about an episode's file.
type Status int

const (
	Missing  Status = iota // not on disk
	Partial                // a .part file exists
	Complete               // present and matches the manifest
)

// Check reports whether the canonical file for e is complete, adopting
// pre-existing files (from older versions or manual copies) into the
// manifest. It returns the status and the absolute destination path.
func (l *Library) Check(e feed.Episode) (Status, string) {
	rel := filepath.FromSlash(e.PathIn(l.layout))
	dst := filepath.Join(l.root, rel)
	mismatch := false

	if en, ok := l.Get(e); ok {
		p := filepath.Join(l.root, filepath.FromSlash(en.Path))
		if fi, err := os.Stat(p); err == nil && fi.Size() == en.Size && en.Size > 0 {
			if p != dst {
				// the naming scheme changed since this was archived: move it
				if err := moveFile(p, dst); err == nil {
					l.Record(e, rel, en.Size)
				} else {
					return Complete, p
				}
			}
			return Complete, dst
		}
		l.Forget(e)
		mismatch = true
	}

	// adopt a file that is already at the canonical path (older versions or
	// manual copies), unless the manifest just told us it is truncated
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 && !mismatch {
		if _, perr := os.Stat(dst + ".part"); perr != nil {
			l.Record(e, rel, fi.Size())
			return Complete, dst
		}
	}
	// look for the same episode under another layout or an older naming
	// scheme ("36.09 - ..." in a season folder, or a dated chronological name)
	if legacy, partial := l.findLegacy(e, dst); legacy != "" && !mismatch {
		if partial {
			if err := moveFile(legacy, dst+".part"); err == nil {
				return Partial, dst
			}
		} else if fi, err := os.Stat(legacy); err == nil && fi.Size() > 0 {
			if err := moveFile(legacy, dst); err == nil {
				l.Record(e, rel, fi.Size())
				return Complete, dst
			}
		}
	}
	if _, err := os.Stat(dst + ".part"); err == nil {
		return Partial, dst
	}
	return Missing, dst
}

// findLegacy returns a file that appears to be this episode under a
// different layout or an older naming scheme, or "". partial reports that
// the match is an unfinished .part file (to be resumed at the new path).
//
// Matching is deliberately strict because MU and MU Plus+ reuse episode
// numbers: a candidate must carry the episode number, the title (when the
// episode has one), and a show marker consistent with the episode.
func (l *Library) findLegacy(e feed.Episode, canonical string) (path string, partial bool) {
	if e.Season == 0 || e.Episode == 0 {
		return "", false
	}
	numRe := regexp.MustCompile(`(^|[^0-9.])` + regexp.QuoteMeta(e.Number()) + `([^0-9]|$)`)
	ext := strings.ToLower(filepath.Ext(e.FileName()))
	titleKey := ""
	if !e.DisplayTitleEmpty() {
		titleKey = strings.ToLower(util.SanitizeFilename(e.Title))
		if len(titleKey) > 40 {
			titleKey = titleKey[:40]
		}
	}
	tag := strings.ToLower(e.ShowTag())

	type place struct {
		dir    string
		chrono bool // shared year folder: the show tag must be in the name
	}
	var places []place
	places = append(places, place{filepath.Join(l.root, e.Show, e.SeasonDir()), false})
	if e.Show == feed.ShowMU {
		// v1 filed everything from the mixed main feed under MU Plus+
		places = append(places, place{filepath.Join(l.root, feed.ShowPlus, e.SeasonDir()), false})
	}
	places = append(places, place{filepath.Dir(filepath.Join(l.root, filepath.FromSlash(e.ChronoPath()))), true})

	var partialHit string
	for _, pl := range places {
		ents, err := os.ReadDir(pl.dir)
		if err != nil {
			continue
		}
		for _, ent := range ents {
			name := ent.Name()
			full := filepath.Join(pl.dir, name)
			if ent.IsDir() || full == canonical || full == canonical+".part" {
				continue
			}
			lname := strings.ToLower(name)
			isPart := strings.HasSuffix(lname, ext+".part")
			if !isPart && !strings.HasSuffix(lname, ext) {
				continue
			}
			if !numRe.MatchString(name) {
				continue
			}
			if titleKey != "" && !strings.Contains(lname, titleKey) {
				continue
			}
			hasPlus := strings.Contains(lname, "plus")
			hasInescapable := strings.Contains(lname, "inescapable")
			switch {
			case pl.chrono:
				// chronological names always carry " - <tag> - " or end in the tag
				if !strings.Contains(lname, " - "+tag+" - ") && !strings.HasSuffix(lname, " - "+tag+ext) && !strings.HasSuffix(lname, " - "+tag+ext+".part") {
					continue
				}
			case e.Show == feed.ShowMU:
				// an MU episode never says "plus" (rules out Plus+ files in the
				// shared v1 folder) and never says "inescapable"
				if hasPlus || hasInescapable {
					continue
				}
			case e.Show == feed.ShowPlus:
				if hasInescapable {
					continue
				}
				// bare-titled Plus+ episodes are only recognisable by the marker
				if titleKey == "" && !hasPlus {
					continue
				}
			case e.Show == feed.ShowInescapable:
				if hasPlus != e.Plus {
					continue
				}
			}
			if isPart {
				if partialHit == "" {
					partialHit = full
				}
				continue
			}
			return full, false
		}
	}
	if partialHit != "" {
		return partialHit, true
	}
	return "", false
}

func moveFile(src, dst string) error {
	if src == dst {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

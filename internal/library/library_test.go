package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mu-dl/internal/feed"
)

func ep() feed.Episode {
	return feed.Episode{Title: "Genius Animals", Show: feed.ShowMU, Season: 36, Episode: 9, EnclosureURL: "https://h/MU_36.09_HQ_x.mp3?sig=1", Length: 999,
		Published: time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)}
}

func TestRecordAndCheck(t *testing.T) {
	root := t.TempDir()
	l, err := Open(root, feed.LayoutSeasons)
	if err != nil {
		t.Fatal(err)
	}
	e := ep()
	st, dst := l.Check(e)
	if st != Missing {
		t.Fatalf("status = %v", st)
	}
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst+".part", []byte("abc"), 0o644)
	if st, _ := l.Check(e); st != Partial {
		t.Fatalf("status = %v, want Partial", st)
	}
	os.Rename(dst+".part", dst)
	l.Record(e, e.RelPath(), 3)
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	l2, _ := Open(root, feed.LayoutSeasons)
	if st, _ := l2.Check(e); st != Complete {
		t.Fatalf("reloaded status = %v", st)
	}
	// a truncated file must be re-downloaded even though the manifest lists it
	os.WriteFile(dst, []byte("a"), 0o644)
	if st, _ := l2.Check(e); st != Missing {
		t.Fatalf("truncated status = %v", st)
	}
}

func TestAdoptLegacyFile(t *testing.T) {
	root := t.TempDir()
	l, _ := Open(root, feed.LayoutSeasons)
	e := ep()
	// older versions filed this MU episode under the Plus+ folder with an en dash
	legacy := filepath.Join(root, "MU Plus+", "Season 36", "36.09 – MU Podcast – Genius Animals.mp3")
	os.MkdirAll(filepath.Dir(legacy), 0o755)
	os.WriteFile(legacy, []byte("data"), 0o644)
	st, dst := l.Check(e)
	if st != Complete {
		t.Fatalf("status = %v", st)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("not moved to canonical path: %v", err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatalf("legacy file still present")
	}
	if l.Count() != 1 || l.TotalBytes() != 4 {
		t.Fatalf("manifest: %d entries, %d bytes", l.Count(), l.TotalBytes())
	}
}

func TestInescapableEditionsNotConfused(t *testing.T) {
	root := t.TempDir()
	l, _ := Open(root, feed.LayoutSeasons)
	free := feed.Episode{Title: "Trust Collapse", Show: feed.ShowInescapable, Season: 1, Episode: 12, EnclosureURL: "https://h/a.mp3"}
	plus := feed.Episode{Title: "Fancy Dress Betrayal", Show: feed.ShowInescapable, Season: 1, Episode: 12, Plus: true, EnclosureURL: "https://h/b.mp3"}
	_, dst := l.Check(plus)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("x"), 0o644)
	if st, _ := l.Check(free); st != Missing {
		t.Fatalf("free edition adopted the plus file: %v", st)
	}
	if st, _ := l.Check(plus); st != Complete {
		t.Fatalf("plus edition status: %v", st)
	}
}

func TestCorruptManifestIsIgnored(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, dirName), 0o755)
	os.WriteFile(filepath.Join(root, dirName, fileName), []byte("{not json"), 0o644)
	if _, err := Open(root, feed.LayoutSeasons); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutSwitchMovesFiles(t *testing.T) {
	root := t.TempDir()
	e := ep()
	seasons, _ := Open(root, feed.LayoutSeasons)
	_, dst := seasons.Check(e)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("audio"), 0o644)
	if st, _ := seasons.Check(e); st != Complete {
		t.Fatal("not adopted")
	}
	seasons.Save()

	chrono, _ := Open(root, feed.LayoutChronological)
	st, newDst := chrono.Check(e)
	if st != Complete {
		t.Fatalf("status after switch = %v", st)
	}
	if want := filepath.Join(root, "2026", "2026-09-03 - 36.09 - MU - Genius Animals.mp3"); newDst != want {
		t.Fatalf("dst = %q", newDst)
	}
	if _, err := os.Stat(newDst); err != nil {
		t.Fatalf("file not moved: %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatalf("old file still there")
	}
	// and back again, without a manifest entry this time (legacy scan)
	os.Remove(filepath.Join(root, dirName, fileName))
	seasons2, _ := Open(root, feed.LayoutSeasons)
	if st, back := seasons2.Check(e); st != Complete || back != dst {
		t.Fatalf("reverse move: %v %q", st, back)
	}
}

func TestPartialFollowsLayout(t *testing.T) {
	root := t.TempDir()
	e := ep()
	seasons, _ := Open(root, feed.LayoutSeasons)
	_, dst := seasons.Check(e)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst+".part", []byte("half"), 0o644)
	chrono, _ := Open(root, feed.LayoutChronological)
	st, newDst := chrono.Check(e)
	if st != Partial {
		t.Fatalf("status = %v", st)
	}
	if _, err := os.Stat(newDst + ".part"); err != nil {
		t.Fatalf(".part not moved: %v", err)
	}
}

func TestSameNumberDifferentShowNotConfused(t *testing.T) {
	root := t.TempDir()
	chrono, _ := Open(root, feed.LayoutChronological)
	mu := feed.Episode{Title: "Alpha", Show: feed.ShowMU, Season: 34, Episode: 9, EnclosureURL: "https://h/a.mp3", Published: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)}
	plus := feed.Episode{Title: "Beta", Show: feed.ShowPlus, Plus: true, Season: 34, Episode: 9, EnclosureURL: "https://h/b.mp3", Published: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)}
	_, pdst := chrono.Check(plus)
	os.MkdirAll(filepath.Dir(pdst), 0o755)
	os.WriteFile(pdst, []byte("x"), 0o644)
	// a legacy MU file with the same number but a different title in a year folder
	if st, _ := chrono.Check(mu); st != Missing {
		t.Fatalf("MU 34.09 wrongly matched the Plus+ file: %v", st)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("x"), 0o644)
}

func TestLegacyMatchingIsStrict(t *testing.T) {
	root := t.TempDir()
	l, _ := Open(root, feed.LayoutSeasons)
	// an Inescapable Plus+ file must never be adopted by bare-titled MU Plus+ 1.01
	ine := filepath.Join(root, feed.ShowInescapable, "Season 01", "1.01 - Restore Britain [Plus+].mp3")
	touch(t, ine)
	plus101 := feed.Episode{Title: "MU Plus+ Podcast", Show: feed.ShowPlus, Plus: true, Season: 1, Episode: 1, EnclosureURL: "https://h/MUP101.mp3", Published: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)}
	if st, _ := l.Check(plus101); st != Missing {
		t.Fatalf("Plus+ 1.01 adopted the Inescapable file: %v", st)
	}
	if _, err := os.Stat(ine); err != nil {
		t.Fatalf("Inescapable file was moved")
	}
	// v1 misfiled bare-titled MU 2.13 under MU Plus+ next to the real Plus+ 2.13
	misfiled := filepath.Join(root, feed.ShowPlus, "Season 02", "2.13 – MU Podcast.mp3")
	realPlus := filepath.Join(root, feed.ShowPlus, "Season 02", "2.13 – MU Plus+ Podcast.mp3")
	touch(t, misfiled)
	touch(t, realPlus)
	mu213 := feed.Episode{Title: "MU Podcast", Show: feed.ShowMU, Season: 2, Episode: 13, EnclosureURL: "https://h/MU213.mp3", Published: time.Date(2009, 12, 3, 0, 0, 0, 0, time.UTC)}
	plus213 := feed.Episode{Title: "MU Plus+ Podcast", Show: feed.ShowPlus, Plus: true, Season: 2, Episode: 13, EnclosureURL: "https://h/MUP213.mp3", Published: time.Date(2009, 12, 7, 0, 0, 0, 0, time.UTC)}
	st, dst := l.Check(mu213)
	if st != Complete || dst != filepath.Join(root, feed.ShowMU, "Season 02", "2.13 - MU Podcast.mp3") {
		t.Fatalf("MU 2.13: %v %q", st, dst)
	}
	if _, err := os.Stat(realPlus); err != nil {
		t.Fatalf("MU 2.13 took the Plus+ file")
	}
	if st, _ := l.Check(plus213); st != Complete {
		t.Fatalf("Plus+ 2.13 not adopted: %v", st)
	}
	// chronological folder: same number, different show tag
	c, _ := Open(root, feed.LayoutChronological)
	touch(t, filepath.Join(root, "2024", "2024-01-04 - 34.09 - MU Plus+ - Beta.mp3"))
	mu := feed.Episode{Title: "Alpha", Show: feed.ShowMU, Season: 34, Episode: 9, EnclosureURL: "https://h/c.mp3", Published: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)}
	if st, _ := c.Check(mu); st != Missing {
		t.Fatalf("chrono cross-show match: %v", st)
	}
}

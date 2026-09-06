package feed

import (
	"os"
	"testing"
	"time"
)

func load(t *testing.T, name, hint string) []Episode {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	eps, err := Parse(data, hint)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return eps
}

func TestParseMainShow(t *testing.T) {
	eps := load(t, "mu_36_hq.xml", "mu")
	if len(eps) != 3 {
		t.Fatalf("want 3 episodes, got %d", len(eps))
	}
	e := eps[0]
	if e.Season != 36 || e.Episode != 9 || e.Show != ShowMU || e.Plus {
		t.Errorf("bad classification: %+v", e)
	}
	if e.Title != "The Strange World of Genius Animals" {
		t.Errorf("Title = %q", e.Title)
	}
	if want := "Mysterious Universe/Season 36/36.09 - The Strange World of Genius Animals.mp3"; e.RelPath() != want {
		t.Errorf("RelPath = %q, want %q", e.RelPath(), want)
	}
	if e.Length != 321956382 || e.Duration != 2*time.Hour+14*time.Minute+2*time.Second {
		t.Errorf("length/duration: %d %v", e.Length, e.Duration)
	}
	// a title with a pipe in it must survive
	if eps[1].Title != "Free-Running | The Human Clock" {
		t.Errorf("pipe title = %q", eps[1].Title)
	}
}

func TestParseOldPlusEpisodesWithEnDash(t *testing.T) {
	eps := load(t, "muplus_1_hq.xml", "muplus")
	e := eps[0]
	if e.Season != 1 || e.Episode != 25 || e.Show != ShowPlus || !e.Plus {
		t.Errorf("bad classification: %+v", e)
	}
	// "1.25 – MU Plus+ Podcast" has no title beyond the marker
	if want := "MU Plus+/Season 01/1.25 - MU Plus+ Podcast.mp3"; e.RelPath() != want {
		t.Errorf("RelPath = %q, want %q", e.RelPath(), want)
	}
}

func TestParseInescapableUsesTitleNumbering(t *testing.T) {
	eps := load(t, "inescapable.xml", "inescapable")
	if len(eps) != 4 {
		t.Fatalf("want 4, got %d", len(eps))
	}
	// itunes:episode says 27 but the title says 1.14 - the title wins
	if eps[0].Season != 1 || eps[0].Episode != 14 || eps[0].Show != ShowInescapable || eps[0].Plus {
		t.Errorf("finale: %+v", eps[0])
	}
	if eps[0].Title != "The Finale" {
		t.Errorf("Title = %q", eps[0].Title)
	}
	// free and Plus+ editions share a number: paths must not collide
	free, plus := eps[3], eps[2]
	if !plus.Plus || free.Plus {
		t.Errorf("plus flags: free=%+v plus=%+v", free, plus)
	}
	if free.RelPath() == plus.RelPath() {
		t.Errorf("collision: %q", free.RelPath())
	}
	if want := "Inescapable/Season 01/1.12 - Fancy Dress Betrayal [Plus+].mp3"; plus.RelPath() != want {
		t.Errorf("plus RelPath = %q, want %q", plus.RelPath(), want)
	}
}

func TestMixedFeedClassifiesPerEpisode(t *testing.T) {
	eps := load(t, "muplushq.xml", "")
	shows := map[string]int{}
	for _, e := range eps {
		shows[e.Show]++
	}
	if shows[ShowMU] != 2 || shows[ShowPlus] != 2 {
		t.Errorf("mixed feed split = %v", shows)
	}
}

func TestDedupAcrossFeeds(t *testing.T) {
	a := load(t, "mu_36_hq.xml", "mu")
	b := load(t, "muplushq.xml", "")
	merged := Dedup(a, b)
	// muplushq shares 36.09 and 36.08 with the season feed
	if len(merged) != 5 {
		t.Fatalf("dedup: want 5, got %d", len(merged))
	}
	dup := a[0]
	dup.EnclosureURL += "?t=DIFFERENT"
	if got := Dedup(a, []Episode{dup}); len(got) != 3 {
		t.Fatalf("query-string variant not collapsed: %d", len(got))
	}
}

func TestSplitTitleVariants(t *testing.T) {
	cases := []struct {
		in    string
		s, e  int
		title string
	}{
		{"36.09 - MU Podcast - The Strange World of Genius Animals", 36, 9, "The Strange World of Genius Animals"},
		{"2.13 – MU Podcast", 2, 13, ""},
		{"+34.01 The First Mystery", 34, 1, "The First Mystery"},
		{"1.12 - Inescapable Plus+ Podcast - Fancy Dress Betrayal", 1, 12, "Fancy Dress Betrayal"},
		{"1.07 - Inescapable - The New Constables", 1, 7, "The New Constables"},
		{"The 3.5 Billion Year Mystery", 0, 0, "The 3.5 Billion Year Mystery"},
		{"Bonus: Q&A", 0, 0, "Bonus: Q&A"},
	}
	for _, c := range cases {
		s, e, title := splitTitle(c.in)
		if s != c.s || e != c.e || title != c.title {
			t.Errorf("splitTitle(%q) = %d,%d,%q; want %d,%d,%q", c.in, s, e, title, c.s, c.e, c.title)
		}
	}
}

func TestBOMAndEncodings(t *testing.T) {
	src := "\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><title>x</title><item><title>3.01 - MU Podcast - Caf\xe9</title><enclosure url=\"https://h/MU_3.01.mp3\" length=\"10\"/></item></channel></rss>"
	eps, err := Parse([]byte(src), "mu")
	if err != nil {
		t.Fatal(err)
	}
	if eps[0].Title != "Café" {
		t.Errorf("latin-1 title = %q", eps[0].Title)
	}
}

func TestChronoPathAndSort(t *testing.T) {
	mu := load(t, "mu_36_hq.xml", "mu")
	plus := load(t, "muplushq.xml", "")
	all := Dedup(mu, plus)
	Sort(all)
	// oldest first, and the two shows interleave by date
	var prev time.Time
	var shows []string
	for _, e := range all {
		if !prev.IsZero() && e.Published.Before(prev) {
			t.Errorf("not chronological: %s after %s", e.Published, prev)
		}
		prev = e.Published
		shows = append(shows, e.Show)
	}
	if shows[0] == shows[1] && shows[1] == shows[2] {
		t.Errorf("expected interleaved shows, got %v", shows)
	}
	e := mu[0]
	if want := "2026/2026-09-03 - 36.09 - MU - The Strange World of Genius Animals.mp3"; e.ChronoPath() != want {
		t.Errorf("ChronoPath = %q, want %q", e.ChronoPath(), want)
	}
	old := load(t, "muplus_1_hq.xml", "muplus")[0]
	if want := "2010/2010-06-07 - 1.25 - MU Plus+.mp3"; old.ChronoPath() != want {
		t.Errorf("bare-title ChronoPath = %q, want %q", old.ChronoPath(), want)
	}
	ine := load(t, "inescapable.xml", "inescapable")[2]
	if want := "2026/2026-05-05 - 1.12 - Inescapable Plus+ - Fancy Dress Betrayal.mp3"; ine.ChronoPath() != want {
		t.Errorf("inescapable ChronoPath = %q, want %q", ine.ChronoPath(), want)
	}
	if e.PathIn(LayoutSeasons) != e.RelPath() || e.PathIn(ParseLayout("")) != e.ChronoPath() {
		t.Errorf("PathIn dispatch wrong")
	}
}

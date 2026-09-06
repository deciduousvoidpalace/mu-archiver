package archiver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mu-dl/internal/config"
)

// fakeSite emulates enough of mysteriousuniverse.org for a full run: the
// identity login form, the dashboard with two seasoned cards and one single
// feed, per-season feeds and MP3 enclosures.
func fakeSite(t *testing.T) *httptest.Server {
	t.Helper()
	dash, _ := os.ReadFile("../dashboard/testdata/dashboard.html")
	var srv *httptest.Server
	mux := http.NewServeMux()
	loginForm := `<form id="account" method="post">
<input type="email" name="Input.Email" value="" />
<input type="password" name="Input.Password" />
<input type="checkbox" checked name="Input.RememberMe" value="true" />
<input name="__RequestVerificationToken" type="hidden" value="T" />
<input name="Input.RememberMe" type="hidden" value="false" />
</form>`
	mux.HandleFunc("/identity/account/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(loginForm))
			return
		}
		r.ParseForm()
		if r.PostForm.Get("Input.Email") == "u@example.com" && r.PostForm.Get("Input.Password") == "pw" {
			http.SetCookie(w, &http.Cookie{Name: ".AspNetCore.Identity.Application", Value: "sess", Path: "/"})
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		w.Write([]byte("<div>Invalid login attempt.</div>" + loginForm))
	})
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(".AspNetCore.Identity.Application"); err != nil || c.Value != "sess" {
			http.Redirect(w, r, "/identity/account/login?ReturnUrl=%2Fdashboard", http.StatusFound)
			return
		}
		// point every feed URL at this server
		body := strings.ReplaceAll(string(dash), "https://feeds.mysteriousuniverse.org", srv.URL)
		w.Write([]byte(body))
	})
	item := func(track string, season, ep int, plus bool) string {
		show := "MU Podcast"
		file := fmt.Sprintf("MU_%d.%02d_HQ.mp3", season, ep)
		if plus {
			show = "MU Plus+ Podcast"
			file = fmt.Sprintf("MUP_%d.%02d_HQ.mp3", season, ep)
		}
		if track == "inescapable" {
			show = "Inescapable Podcast"
			file = fmt.Sprintf("INE_%d.%02d.mp3", season, ep)
		}
		// a plausible release date: seasons are ~6 months apart, MU on
		// Thursdays and Plus+ on the following Monday of the same week
		day := time.Date(2009, 1, 1, 7, 0, 0, 0, time.UTC).AddDate(0, 6*(season-1), 7*(ep-1))
		if plus {
			day = day.AddDate(0, 0, 4)
		}
		return fmt.Sprintf(`<item><title>%d.%02d - %s - Title %d</title><guid>g-%s-%d-%d</guid><pubDate>%s</pubDate><itunes:season>%d</itunes:season><itunes:episode>%d</itunes:episode>
<enclosure url="%s/media/%s" length="0" type="audio/mpeg"/></item>`, season, ep, show, ep, track, season, ep, day.Format(time.RFC1123Z), season, ep, srv.URL, file)
	}
	mux.HandleFunc("/feed/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		w.Header().Set("Content-Type", "application/rss+xml")
		head := "\xEF\xBB\xBF<?xml version=\"1.0\"?><rss xmlns:itunes=\"http://www.itunes.com/dtds/podcast-1.0.dtd\"><channel><title>%s</title>%s</channel></rss>"
		switch {
		case len(parts) == 5 && (parts[1] == "mu" || parts[1] == "muplus"):
			season := 0
			fmt.Sscanf(parts[2], "%d", &season)
			if season > 3 && season != 36 && season != 34 {
				w.Write(nil) // empty 200 like the real site for absent seasons
				return
			}
			var items []string
			for ep := 1; ep <= 2; ep++ {
				items = append(items, item(parts[1], season, ep, parts[1] == "muplus"))
			}
			fmt.Fprintf(w, head, parts[1]+" "+parts[2], strings.Join(items, ""))
		case len(parts) == 3 && parts[1] == "inescapable":
			fmt.Fprintf(w, head, "Inescapable", item("inescapable", 1, 1, false))
		case len(parts) == 3 && parts[1] == "muplushq":
			// mixed main feed overlapping with season feeds
			fmt.Fprintf(w, head, "MU Plus+ HQ", item("mu", 36, 1, false)+item("muplus", 34, 2, true))
		default:
			w.WriteHeader(404)
		}
	})
	mux.HandleFunc("/media/", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		data := []byte(strings.Repeat(name, 100))
		w.Header().Set("Content-Type", "audio/mpeg")
		http.ServeContent(w, r, name, mtime, strings.NewReader(string(data)))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFullRunAgainstFakeSite(t *testing.T) {
	srv := fakeSite(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out := t.TempDir()
	cfg := config.Defaults()
	cfg.BaseURL = srv.URL
	cfg.FeedHost = strings.TrimPrefix(srv.URL, "http://")
	cfg.OutDir = out
	cfg.Email, cfg.Password = "u@example.com", "pw"
	cfg.RequestGapMS, cfg.EpisodeGapSec = 0, 0
	cfg.Concurrency = 1 // deterministic completion order

	eng := New(cfg)
	var evMu sync.Mutex
	var events []Event
	eng.Subscribe(func(ev Event) { evMu.Lock(); events = append(events, ev); evMu.Unlock() })

	sum, err := eng.Run(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// mu seasons 2,3,34,36 (2 eps each) + muplus 1,2,3,34 (2 each) + inescapable 1
	// = 8 + 8 + 1 = 17 unique (main feed overlaps fully)
	if sum.Total != 17 || sum.Downloaded != 17 || sum.Failed != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	// default layout is one chronological timeline
	want := filepath.Join(out, "2026", "2026-07-01 - 36.01 - MU - Title 1.mp3")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "2025", "2025-07-12 - 34.02 - MU Plus+ - Title 2.mp3")); err != nil {
		t.Errorf("plus file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "2009", "2009-01-01 - 1.01 - Inescapable - Title 1.mp3")); err != nil {
		t.Errorf("inescapable file missing: %v", err)
	}
	// downloads happen oldest-first, MU and Plus+ interleaved by date
	var order []time.Time
	evMu.Lock()
	for _, ev := range events {
		if ev.Kind == KindEpisode && ev.Episode != nil {
			order = append(order, ev.Episode.Published)
		}
	}
	evMu.Unlock()
	if len(order) != 17 {
		t.Fatalf("episode events = %d", len(order))
	}
	for i := 1; i < len(order); i++ {
		if order[i].Before(order[i-1]) {
			t.Fatalf("download order not chronological at %d: %v", i, order)
		}
	}
	if !order[0].Equal(time.Date(2009, 1, 1, 7, 0, 0, 0, time.UTC)) || !order[len(order)-1].Equal(time.Date(2026, 7, 8, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("download order bounds wrong: first=%s last=%s", order[0], order[len(order)-1])
	}
	st := eng.Status()
	if st.Plan != "MU Max+ Yearly" || !st.LoggedIn || st.Archived != 17 {
		t.Errorf("status = %+v", st)
	}

	// second run: everything is skipped, session cookie reused (no login event)
	evMu.Lock()
	events = nil
	evMu.Unlock()
	sum, err = eng.Run(context.Background(), RunOptions{})
	if err != nil || sum.Downloaded != 0 || sum.Skipped != 17 {
		t.Fatalf("second run: %+v err=%v", sum, err)
	}
	evMu.Lock()
	for _, ev := range events {
		if ev.Kind == KindLoginOK {
			t.Errorf("re-login happened despite a valid session")
		}
	}
	evMu.Unlock()

	// switching layouts moves the files instead of re-downloading them
	cfg.Layout = "seasons"
	eng.SetConfig(cfg)
	sum, err = eng.Run(context.Background(), RunOptions{})
	if err != nil || sum.Downloaded != 0 || sum.Skipped != 17 {
		t.Fatalf("layout switch run: %+v err=%v", sum, err)
	}
	if _, err := os.Stat(filepath.Join(out, "Mysterious Universe", "Season 36", "36.01 - Title 1.mp3")); err != nil {
		t.Errorf("file not moved to season layout: %v", err)
	}
	if _, err := os.Stat(want); err == nil {
		t.Errorf("chronological file still present after switch")
	}
	cfg.Layout = "chronological"
	eng.SetConfig(cfg)
	if sum, err = eng.Run(context.Background(), RunOptions{}); err != nil || sum.Downloaded != 0 {
		t.Fatalf("switch back: %+v err=%v", sum, err)
	}

	// third run: a truncated file is repaired
	os.WriteFile(want, []byte("xx"), 0o644)
	sum, err = eng.Run(context.Background(), RunOptions{})
	if err != nil || sum.Downloaded != 1 {
		t.Fatalf("repair run: %+v err=%v", sum, err)
	}
	if fi, _ := os.Stat(want); fi.Size() <= 2 {
		t.Errorf("file not repaired")
	}

	// filters: only season 2 of muplus, scan only
	cfg.Shows = []string{"muplus"}
	eng.SetConfig(cfg)
	sum, err = eng.Run(context.Background(), RunOptions{ScanOnly: true, Season: 2})
	if err != nil || sum.Total != 2 {
		t.Fatalf("filtered scan: %+v err=%v", sum, err)
	}
	// config-level season range must also exclude what the mixed main feed
	// carries (it has 36.01 and 34.02)
	cfg.Shows = []string{"mu"}
	cfg.MinSeason, cfg.MaxSeason = 2, 3
	eng.SetConfig(cfg)
	sum, err = eng.Run(context.Background(), RunOptions{ScanOnly: true})
	if err != nil || sum.Total != 4 {
		t.Fatalf("season-range scan: %+v err=%v", sum, err)
	}
	for _, it := range eng.Items() {
		if it.Show != "Mysterious Universe" || it.Season < 2 || it.Season > 3 {
			t.Errorf("leaked through filter: %+v", it.Episode)
		}
	}
}

func TestBadCredentials(t *testing.T) {
	srv := fakeSite(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.BaseURL = srv.URL
	cfg.OutDir = t.TempDir()
	cfg.Email, cfg.Password = "u@example.com", "nope"
	cfg.RequestGapMS = 0
	eng := New(cfg)
	if _, err := eng.Run(context.Background(), RunOptions{}); err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("err = %v", err)
	}
	if _, err := eng.TestLogin(context.Background(), "u@example.com", "pw"); err != nil {
		t.Fatalf("TestLogin with good creds: %v", err)
	}
}

func TestStopMidRun(t *testing.T) {
	srv := fakeSite(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.BaseURL = srv.URL
	cfg.OutDir = t.TempDir()
	cfg.Email, cfg.Password = "u@example.com", "pw"
	cfg.RequestGapMS = 200 // slow enough to interrupt
	eng := New(cfg)
	if err := eng.Start(RunOptions{}); err != nil {
		t.Fatal(err)
	}
	for eng.Status().State != StateScanning && eng.Status().State != StateDownloading {
		if !eng.Running() {
			t.Fatal("run finished before it could be stopped")
		}
	}
	eng.Stop()
	eng.Wait()
	if eng.Running() || eng.Status().State != StateIdle {
		t.Fatalf("after stop: %+v", eng.Status())
	}
}

var mtime = mustTime()

func mustTime() (t time.Time) { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// TestStaleSavedFeedsIgnoredWhenLoggedIn guards the regression that produced
// "no feeds to archive": a leftover v1 cfg.Feeds list must not pin or empty a
// run once an account is configured. The dashboard is authoritative there.
func TestStaleSavedFeedsIgnoredWhenLoggedIn(t *testing.T) {
	srv := fakeSite(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.BaseURL = srv.URL
	cfg.FeedHost = strings.TrimPrefix(srv.URL, "http://")
	cfg.OutDir = t.TempDir()
	cfg.Email, cfg.Password = "u@example.com", "pw"
	cfg.RequestGapMS, cfg.EpisodeGapSec = 0, 0
	cfg.Concurrency = 1
	// A stale saved feed of the opposite quality: under the old code this
	// took the cfg.Feeds branch, FilterQuality emptied it, and the run failed.
	cfg.Quality = "hq"
	cfg.Feeds = []string{srv.URL + "/feed/mu/2/sq/tok"}

	eng := New(cfg)
	sum, err := eng.Run(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("run with stale saved feeds: %v", err)
	}
	if sum.Total == 0 {
		t.Fatalf("expected the dashboard catalogue, got nothing")
	}
}

// TestNoSourcesErrorNamesFilter checks that the zero-match diagnostic names the
// active show and season filter instead of the old catch-all message.
func TestNoSourcesErrorNamesFilter(t *testing.T) {
	cfg := config.Defaults()
	cfg.Shows = []string{"mu"}
	cfg.MinSeason, cfg.MaxSeason = 10, 12
	err := noSourcesError(cfg, RunOptions{}, true)
	for _, want := range []string{"Mysterious Universe", "10", "12"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
	// A per-run season override is reflected too.
	err = noSourcesError(cfg, RunOptions{Season: 5}, true)
	if !strings.Contains(err.Error(), "seasons 5") {
		t.Fatalf("override not reflected: %v", err)
	}
}

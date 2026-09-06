// Package catalogue turns the parsed dashboard into the concrete list of feed
// URLs to archive: one per season per show for the seasoned tracks, plus the
// single-feed shows (Inescapable) and the main mixed feed. When the dashboard
// cannot be parsed but a token is known, it falls back to probing the
// well-known URL pattern
//
//	https://<feedHost>/feed/<track>/<season>/<quality>/<token>
package catalogue

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"mu-dl/internal/dashboard"
	"mu-dl/internal/httpx"
)

// Source is one feed to fetch.
type Source struct {
	URL     string
	Track   string // "mu", "muplus", "inescapable", "" (mixed)
	Show    string // display name for the track
	Season  int    // 0 for unseasoned feeds
	Quality string // "hq" or "sq"
}

// Label is a short human description of the source.
func (s Source) Label() string {
	if s.Season > 0 {
		return fmt.Sprintf("%s · Season %d (%s)", s.Show, s.Season, strings.ToUpper(s.Quality))
	}
	return fmt.Sprintf("%s (%s)", s.Show, strings.ToUpper(s.Quality))
}

// Filter narrows what gets archived.
type Filter struct {
	Quality   string   // "hq" (default) or "sq"
	Shows     []string // track names to include; empty = all
	MinSeason int
	MaxSeason int
}

// ShowName maps a track to its display name.
func ShowName(track string) string {
	switch track {
	case "mu":
		return "Mysterious Universe"
	case "muplus":
		return "MU Plus+"
	case "inescapable":
		return "Inescapable"
	case "muplushq", "muplussq":
		return "MU Plus+ (current seasons)"
	}
	return strings.ToUpper(track[:1]) + track[1:]
}

// KnownTracks are the show tracks the archiver understands, in display order.
var KnownTracks = []string{"mu", "muplus", "inescapable"}

func (f Filter) wantsTrack(track string) bool {
	if len(f.Shows) == 0 {
		return true
	}
	for _, s := range f.Shows {
		if s == track {
			return true
		}
		// the mixed main feed belongs to both mu and muplus
		if (track == "muplushq" || track == "muplussq") && (s == "mu" || s == "muplus") {
			return true
		}
	}
	return false
}

func (f Filter) wantsSeason(n int) bool {
	if f.MinSeason > 0 && n < f.MinSeason {
		return false
	}
	if f.MaxSeason > 0 && n > f.MaxSeason {
		return false
	}
	return true
}

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// ExtractToken returns the first per-user token (UUID) found in a list of
// feed URLs, or "" if none is present.
func ExtractToken(feeds []string) string {
	for _, f := range feeds {
		if m := uuidRe.FindString(f); m != "" {
			return m
		}
	}
	return ""
}

// TokenValid reports whether s looks like a feed token.
func TokenValid(s string) bool {
	return uuidRe.MatchString(strings.TrimSpace(s))
}

// BuildURL constructs a single season feed URL.
func BuildURL(feedHost, track string, season int, quality, token string) string {
	if quality != "sq" {
		quality = "hq"
	}
	return fmt.Sprintf("https://%s/feed/%s/%d/%s/%s", strings.TrimRight(feedHost, "/"), track, season, quality, token)
}

// FromDashboard builds the source list from the parsed dashboard.
func FromDashboard(d *dashboard.Dashboard, f Filter) []Source {
	quality := "hq"
	if f.Quality == "sq" {
		quality = "sq"
	}
	var out []Source
	seen := map[string]bool{}
	add := func(s Source) {
		if s.URL == "" || seen[s.URL] {
			return
		}
		seen[s.URL] = true
		out = append(out, s)
	}
	for _, c := range d.Cards {
		if c.Track == "" || !f.wantsTrack(c.Track) {
			continue
		}
		if c.Seasoned {
			for _, n := range c.Seasons {
				if !f.wantsSeason(n) {
					continue
				}
				u := c.HQ
				if quality == "sq" && c.SQ != "" {
					u = c.SQ
				}
				add(Source{URL: replaceSeason(u, n), Track: c.Track, Show: ShowName(c.Track), Season: n, Quality: quality})
			}
			continue
		}
		u := c.HQ
		q := quality
		if quality == "sq" {
			if c.SQ != "" {
				u = c.SQ
			} else {
				q = "hq" // single-quality feed
			}
		}
		add(Source{URL: u, Track: c.Track, Show: ShowName(c.Track), Quality: q})
	}
	// put the mixed main feed last so season-specific feeds are parsed first
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].Season == 0 && strings.HasPrefix(out[i].Track, "muplus") && out[i].Track != "muplus", out[j].Season == 0 && strings.HasPrefix(out[j].Track, "muplus") && out[j].Track != "muplus"
		return !mi && mj
	})
	return out
}

var seasonSegRe = regexp.MustCompile(`/(\d+)/(hq|sq)/`)

// replaceSeason swaps the season segment of a seasoned feed URL, mirroring
// the dashboard's own JavaScript.
func replaceSeason(u string, season int) string {
	return seasonSegRe.ReplaceAllString(u, fmt.Sprintf("/%d/$2/", season))
}

// Probe requests each candidate season feed sequentially (through the rate
// limiter) and returns the sources that contain at least one item. It is the
// fallback used when the dashboard could not be parsed; stop is called after
// stopAfter consecutive empty seasons per track.
func Probe(ctx context.Context, c *httpx.Client, feedHost, token string, tracks []string, f Filter, stopAfter int, progress func(Source, bool)) []Source {
	if stopAfter <= 0 {
		stopAfter = 3
	}
	quality := "hq"
	if f.Quality == "sq" {
		quality = "sq"
	}
	max := f.MaxSeason
	if max <= 0 {
		max = 60
	}
	min := f.MinSeason
	if min <= 0 {
		min = 1
	}
	var out []Source
	for _, tr := range tracks {
		if !f.wantsTrack(tr) {
			continue
		}
		misses := 0
		for n := min; n <= max; n++ {
			if ctx.Err() != nil {
				return out
			}
			s := Source{URL: BuildURL(feedHost, tr, n, quality, token), Track: tr, Show: ShowName(tr), Season: n, Quality: quality}
			body, resp, err := c.GetString(ctx, s.URL)
			ok := err == nil && resp != nil && resp.StatusCode == 200 && strings.Contains(body, "<item")
			if progress != nil {
				progress(s, ok)
			}
			if ok {
				out = append(out, s)
				misses = 0
			} else {
				misses++
				if misses >= stopAfter && n > 5 {
					break
				}
			}
		}
	}
	return out
}

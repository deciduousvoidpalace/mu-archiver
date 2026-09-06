// Package feed parses podcast RSS feeds into a normalized list of episodes.
// It understands the Mysterious Universe conventions: the "SS.EE - Show -
// Title" numbering in item titles (which is authoritative; the iTunes
// episode tags are not), the three shows (Mysterious Universe, MU Plus+ and
// Inescapable) that may share a single feed, and the overlap between the main
// feed and the per-season feeds.
package feed

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	"mu-dl/internal/httpx"
	"mu-dl/internal/util"
)

// Show names used as top-level folders.
const (
	ShowMU          = "Mysterious Universe"
	ShowPlus        = "MU Plus+"
	ShowInescapable = "Inescapable"
)

// Episode is a single downloadable item.
type Episode struct {
	Title        string // cleaned title, e.g. "The Strange World of Genius Animals"
	RawTitle     string // title exactly as published
	GUID         string
	EnclosureURL string
	MIMEType     string
	Length       int64
	Season       int  // 0 == unknown
	Episode      int  // 0 == unknown
	Plus         bool // Plus+ member edition (Inescapable numbers both editions the same)
	Duration     time.Duration
	Published    time.Time
	FeedTitle    string
	FeedURL      string
	Show         string // one of the Show* constants
}

// --- XML structs -------------------------------------------------------

const itunesNS = "http://www.itunes.com/dtds/podcast-1.0.dtd"

type rssDoc struct {
	Channel struct {
		Title string    `xml:"title"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

// The itunes-qualified fields are declared before the unqualified ones on
// purpose: encoding/xml binds an element to the first matching field, and an
// unqualified tag matches any namespace, so this order routes <itunes:title>
// to ITunesTitle and leaves <title> for Title.
type rssItem struct {
	ITunesTitle   string       `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd title"`
	ITunesSeason  string       `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd season"`
	ITunesEpisode string       `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
	Duration      string       `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	Title         string       `xml:"title"`
	GUID          string       `xml:"guid"`
	PubDate       string       `xml:"pubDate"`
	Enclosure     rssEnclosure `xml:"enclosure"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

var (
	// "36.09 - MU Podcast - Title", "+34.01 Title", "2.13 – MU Podcast"
	numRe = regexp.MustCompile(`^\s*\+?(\d{1,3})\.(\d{1,3})\s*(?:[-–—:|]\s*)?(.*)$`)
	// leading show marker to strip from the remainder of the title
	markerRe = regexp.MustCompile(`(?i)^(?:(?:the\s+)?(?:mysterious universe|mu)(?:\s*plus\+?)?|inescapable(?:\s*plus\+?)?)(?:\s*podcast)?\s*(?:[-–—:|]\s*|$)`)
	plusRe   = regexp.MustCompile(`(?i)\bplus\+?(?:\s|$|\W)`)
)

var pubDateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05Z07:00",
}

func parsePubDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range pubDateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	var secs int
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0
		}
		secs = secs*60 + n
	}
	return time.Duration(secs) * time.Second
}

// Parse turns raw feed XML into episodes. trackHint is the show track the
// feed was requested for ("mu", "muplus", "inescapable", or "" for mixed
// feeds); it is only used when the title does not identify the show.
func Parse(data []byte, trackHint string) ([]Episode, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel
	dec.Strict = false
	var doc rssDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing feed XML: %w", err)
	}
	var eps []Episode
	for _, it := range doc.Channel.Items {
		if strings.TrimSpace(it.Enclosure.URL) == "" {
			continue // nothing to download
		}
		raw := strings.TrimSpace(it.Title)
		if raw == "" {
			raw = strings.TrimSpace(it.ITunesTitle)
		}
		ep := Episode{
			RawTitle:     raw,
			GUID:         strings.TrimSpace(it.GUID),
			EnclosureURL: strings.TrimSpace(it.Enclosure.URL),
			MIMEType:     it.Enclosure.Type,
			Duration:     parseDuration(it.Duration),
			Published:    parsePubDate(it.PubDate),
			FeedTitle:    strings.TrimSpace(doc.Channel.Title),
		}
		ep.Length, _ = strconv.ParseInt(strings.TrimSpace(it.Enclosure.Length), 10, 64)
		ep.Season, ep.Episode, ep.Title = splitTitle(raw)
		if ep.Season == 0 {
			ep.Season, _ = strconv.Atoi(strings.TrimSpace(it.ITunesSeason))
		}
		if ep.Episode == 0 {
			ep.Episode, _ = strconv.Atoi(strings.TrimSpace(it.ITunesEpisode))
		}
		ep.Show, ep.Plus = classify(raw, ep.EnclosureURL, trackHint, doc.Channel.Title)
		if ep.Title == "" {
			ep.Title = strings.TrimSpace(it.ITunesTitle)
		}
		if ep.Title == "" {
			ep.Title = raw
		}
		eps = append(eps, ep)
	}
	return eps, nil
}

// splitTitle extracts "SS.EE" numbering and the human title.
func splitTitle(raw string) (season, episode int, title string) {
	m := numRe.FindStringSubmatch(raw)
	if m == nil {
		return 0, 0, cleanTitle(raw)
	}
	season, _ = strconv.Atoi(m[1])
	episode, _ = strconv.Atoi(m[2])
	return season, episode, cleanTitle(m[3])
}

// cleanTitle strips the leading show marker ("MU Podcast - ", "Inescapable
// Plus+ - ") from a title remainder.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if loc := markerRe.FindStringIndex(s); loc != nil {
		s = strings.TrimSpace(s[loc[1]:])
	}
	return strings.Trim(s, " -–—:|")
}

// classify determines the show (folder) and the Plus+ flag.
func classify(rawTitle, encURL, trackHint, channelTitle string) (show string, plus bool) {
	lt := strings.ToLower(rawTitle)
	plus = plusRe.MatchString(rawTitle)
	switch {
	case strings.Contains(lt, "inescapable"):
		return ShowInescapable, plus
	case plus:
		return ShowPlus, true
	case strings.Contains(lt, "mu podcast") || strings.Contains(lt, "mysterious universe"):
		return ShowMU, false
	}
	// title didn't say: use the enclosure filename convention (MUP_ vs MU_)
	base := strings.ToUpper(path.Base(encURL))
	switch {
	case strings.HasPrefix(base, "MUP"):
		return ShowPlus, true
	case strings.HasPrefix(base, "MU"):
		return ShowMU, false
	}
	switch trackHint {
	case "muplus":
		return ShowPlus, true
	case "inescapable":
		return ShowInescapable, false
	case "mu":
		return ShowMU, false
	}
	if strings.Contains(strings.ToLower(channelTitle), "inescapable") {
		return ShowInescapable, false
	}
	return ShowMU, false
}

// Cache stores fetched feed XML so repeat scans use conditional requests
// (If-None-Match / If-Modified-Since) and fall back to the last good copy
// when the network is unavailable.
type Cache interface {
	Get(url string) (body []byte, etag, lastModified string, ok bool)
	Put(url string, body []byte, etag, lastModified string)
}

// Fetch downloads and parses a single feed URL. cache may be nil.
func Fetch(ctx context.Context, c *httpx.Client, feedURL, trackHint string, cache Cache) ([]Episode, bool, error) {
	headers := map[string]string{}
	var cached []byte
	if cache != nil {
		if body, etag, lm, ok := cache.Get(feedURL); ok {
			cached = body
			if etag != "" {
				headers["If-None-Match"] = etag
			}
			if lm != "" {
				headers["If-Modified-Since"] = lm
			}
		}
	}
	body, resp, err := c.GetBytes(ctx, feedURL, headers)
	fromCache := false
	switch {
	case err != nil && cached != nil && ctx.Err() == nil:
		body, fromCache = cached, true
	case err != nil:
		return nil, false, err
	case resp.StatusCode == 304 && cached != nil:
		body, fromCache = cached, true
	case resp.StatusCode >= 400:
		return nil, false, fmt.Errorf("feed returned %s", resp.Status)
	case len(bytes.TrimSpace(body)) == 0:
		// MU returns an empty 200 for seasons that don't exist
		return nil, false, nil
	default:
		if cache != nil {
			cache.Put(feedURL, body, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"))
		}
	}
	eps, err := Parse(body, trackHint)
	if err != nil {
		return nil, fromCache, err
	}
	for i := range eps {
		eps[i].FeedURL = feedURL
	}
	return eps, fromCache, nil
}

// Key identifies an episode across overlapping feeds. The enclosure URL
// (minus query string, which may carry per-request tokens) is the most
// reliable key; GUID is used when the URL is unusable.
func (e Episode) Key() string {
	u := e.EnclosureURL
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if u != "" {
		return "u:" + strings.ToLower(u)
	}
	return "g:" + e.GUID
}

// Dedup merges episode lists, keeping the first occurrence of each key and
// upgrading it with metadata from later duplicates.
func Dedup(lists ...[]Episode) []Episode {
	index := map[string]int{}
	var out []Episode
	for _, list := range lists {
		for _, e := range list {
			k := e.Key()
			if k == "u:" || k == "g:" {
				out = append(out, e)
				continue
			}
			if i, ok := index[k]; ok {
				if out[i].Season == 0 && e.Season != 0 {
					out[i].Season = e.Season
				}
				if out[i].Episode == 0 && e.Episode != 0 {
					out[i].Episode = e.Episode
				}
				if out[i].Length == 0 && e.Length != 0 {
					out[i].Length = e.Length
				}
				continue
			}
			index[k] = len(out)
			out = append(out, e)
		}
	}
	return out
}

// Sort orders episodes chronologically by publication date across all shows
// (so MU and MU Plus+ episodes interleave the way they were released),
// falling back to show/season/episode for undated items.
func Sort(eps []Episode) {
	sort.SliceStable(eps, func(i, j int) bool {
		a, b := eps[i], eps[j]
		if !a.Published.Equal(b.Published) {
			if a.Published.IsZero() {
				return false
			}
			if b.Published.IsZero() {
				return true
			}
			return a.Published.Before(b.Published)
		}
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		if a.Show != b.Show {
			return a.Show < b.Show
		}
		return !a.Plus && b.Plus
	})
}

// Layout selects how files are arranged under the archive root.
type Layout string

// Layouts.
const (
	// LayoutChronological puts every show on one timeline:
	// "2026/2026-09-03 - 36.09 - MU - Title.mp3".
	LayoutChronological Layout = "chronological"
	// LayoutSeasons groups by show and season:
	// "Mysterious Universe/Season 36/36.09 - Title.mp3".
	LayoutSeasons Layout = "seasons"
)

// ParseLayout normalises a layout name (default chronological).
func ParseLayout(s string) Layout {
	if strings.EqualFold(strings.TrimSpace(s), string(LayoutSeasons)) {
		return LayoutSeasons
	}
	return LayoutChronological
}

// ShowTag is the short show marker used in chronological file names.
func (e Episode) ShowTag() string {
	switch e.Show {
	case ShowPlus:
		return "MU Plus+"
	case ShowInescapable:
		if e.Plus {
			return "Inescapable Plus+"
		}
		return "Inescapable"
	}
	return "MU"
}

// PathIn returns the relative path for the episode under the given layout.
func (e Episode) PathIn(l Layout) string {
	if l == LayoutSeasons {
		return e.RelPath()
	}
	return e.ChronoPath()
}

// ChronoPath returns the chronological-layout relative path, e.g.
// "2026/2026-09-03 - 36.09 - MU Plus+ - The Endless Day.mp3".
func (e Episode) ChronoPath() string {
	ext := util.ExtFromURL(e.EnclosureURL, ".mp3")
	var parts []string
	dir := "Undated"
	if !e.Published.IsZero() {
		parts = append(parts, e.Published.UTC().Format("2006-01-02"))
		dir = e.Published.UTC().Format("2006")
	}
	if n := e.Number(); n != "" {
		parts = append(parts, n)
	}
	parts = append(parts, e.ShowTag())
	if t := e.displayTitle(); t != "" {
		parts = append(parts, t)
	}
	return path.Join(dir, util.SanitizeFilename(strings.Join(parts, " - "))+ext)
}

// DisplayTitleEmpty reports whether the episode has no title beyond the
// show marker (early seasons were published as just "2.13 – MU Podcast").
func (e Episode) DisplayTitleEmpty() bool { return e.displayTitle() == "" }

// displayTitle is the title without a redundant bare show marker.
func (e Episode) displayTitle() string {
	t := strings.TrimSpace(e.Title)
	switch strings.ToLower(t) {
	case "mu podcast", "mu plus+ podcast", "mu plus podcast", "mysterious universe", "inescapable", "inescapable podcast", "inescapable plus+ podcast":
		return ""
	}
	return t
}

// Number renders the "SS.EE" episode number ("" if unknown).
func (e Episode) Number() string {
	if e.Season > 0 && e.Episode > 0 {
		return fmt.Sprintf("%d.%02d", e.Season, e.Episode)
	}
	return ""
}

// FileName is the canonical file name for the episode.
func (e Episode) FileName() string {
	ext := util.ExtFromURL(e.EnclosureURL, ".mp3")
	title := e.Title
	if title == "" {
		title = strings.TrimSuffix(path.Base(e.EnclosureURL), util.ExtFromURL(e.EnclosureURL, ""))
	}
	name := title
	if n := e.Number(); n != "" {
		name = n + " - " + title
	}
	if e.Show == ShowInescapable && e.Plus {
		name += " [Plus+]"
	}
	return util.SanitizeFilename(name) + ext
}

// SeasonDir is the season folder name ("Season 36", or a year / "Unsorted").
func (e Episode) SeasonDir() string {
	switch {
	case e.Season > 0:
		return fmt.Sprintf("Season %02d", e.Season)
	case !e.Published.IsZero():
		return fmt.Sprintf("%04d", e.Published.Year())
	default:
		return "Unsorted"
	}
}

// RelPath returns the show/season-organized relative path (without the
// output root), e.g. "Mysterious Universe/Season 36/36.09 - Title.mp3".
func (e Episode) RelPath() string {
	show := e.Show
	if show == "" {
		show = ShowMU
	}
	return path.Join(show, e.SeasonDir(), e.FileName())
}

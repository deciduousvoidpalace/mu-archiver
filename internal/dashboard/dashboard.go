// Package dashboard scrapes the authenticated /dashboard page. The page
// renders one "gray box" card per podcast feed, each carrying hidden inputs
// with the tokenized HQ/SQ feed URLs, an rss-id, a heading, and (for the
// per-season shows) a <select> listing every season the subscription grants.
// Reading that select is what lets the archiver enumerate the whole
// back-catalogue without guessing.
package dashboard

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"mu-dl/internal/httpx"
)

// Card is one feed card on the dashboard.
type Card struct {
	ID       string
	Name     string // e.g. "MU Extended Podcasts"
	Subtitle string // e.g. "All Seasons"
	HQ       string // HQ feed URL (may be the only URL)
	SQ       string // SQ feed URL ("" if the card has no SQ variant)
	Track    string // "mu", "muplus", "inescapable", "muplushq", ...
	Seasoned bool   // URL carries a season segment and the card lists seasons
	Seasons  []int  // seasons offered (descending as rendered, we sort ascending)
}

// Dashboard is the parsed page.
type Dashboard struct {
	Plan  string // e.g. "MU Max+ Yearly"
	Token string // per-user feed token (UUID)
	Cards []Card
	// Feeds is every feed-looking URL found on the page (fallback for
	// unknown layouts).
	Feeds []string
}

var (
	uuidRe    = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	seasonRe  = regexp.MustCompile(`^/feed/([a-z0-9]+)/(\d+)/(hq|sq)/` + uuidRe.String() + `$`)
	singleRe  = regexp.MustCompile(`^/feed/([a-z0-9]+)/` + uuidRe.String() + `$`)
	hiddenRe  = regexp.MustCompile(`(?is)<input[^>]*\bid="(Clipboard|Phone)(HQ|SQ)-(\d+)"[^>]*\bvalue="([^"]+)"`)
	rssIDRe   = regexp.MustCompile(`(?is)<input[^>]*class="rss-id"[^>]*value="(\d+)"`)
	h3Re      = regexp.MustCompile(`(?is)<h3>(.*?)</h3>`)
	spanRe    = regexp.MustCompile(`(?is)<span class="first-part">(.*?)</span>\s*<span class="second-part">(.*?)</span>`)
	selectRe  = regexp.MustCompile(`(?is)<select[^>]*season-selector[^>]*>(.*?)</select>`)
	optionRe  = regexp.MustCompile(`(?is)<option[^>]*value="(\d+)"`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]+>`)
	planRe    = regexp.MustCompile(`(?i)MU\s+(Max|Plus)(?:&#x2B;|&#43;|\+)?\s+(Yearly|Monthly|Annual)`)
	anyFeedRe = regexp.MustCompile(`https?://[^\s"'<>)]+/feed[^\s"'<>)]*`)
	loginRe   = regexp.MustCompile(`(?i)/identity/account/login`)
)

// Fetch downloads the dashboard (requires an authenticated, redirect-following
// client) and parses it.
func Fetch(ctx context.Context, c *httpx.Client, baseURL string) (*Dashboard, error) {
	dashURL := strings.TrimRight(baseURL, "/") + "/dashboard"
	body, resp, err := c.GetString(ctx, dashURL)
	if err != nil {
		return nil, fmt.Errorf("fetching dashboard: %w", err)
	}
	if resp != nil && resp.Request != nil && loginRe.MatchString(resp.Request.URL.Path) {
		return nil, fmt.Errorf("not logged in: dashboard redirected to the login page")
	}
	return Parse(body)
}

// Parse extracts the feed cards, token and plan from dashboard HTML.
func Parse(body string) (*Dashboard, error) {
	d := &Dashboard{}
	if m := planRe.FindStringSubmatch(body); m != nil {
		d.Plan = "MU " + strings.Title(strings.ToLower(m[1])) + "+ " + strings.Title(strings.ToLower(m[2]))
	}

	// gather hidden feed inputs by card id
	type urls struct{ hq, sq string }
	byID := map[string]*urls{}
	var order []string
	for _, m := range hiddenRe.FindAllStringSubmatch(body, -1) {
		id, q, raw := m[3], m[2], html.UnescapeString(m[4])
		u := byID[id]
		if u == nil {
			u = &urls{}
			byID[id] = u
			order = append(order, id)
		}
		if q == "HQ" && u.hq == "" {
			u.hq = raw
		}
		if q == "SQ" && u.sq == "" {
			u.sq = raw
		}
	}

	// split the page into card segments at each rss-id marker so headings and
	// season selects can be associated with the right card
	locs := rssIDRe.FindAllStringSubmatchIndex(body, -1)
	segments := map[string]string{}
	for i, loc := range locs {
		id := body[loc[2]:loc[3]]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		segments[id] = body[loc[1]:end]
	}

	for _, id := range order {
		u := byID[id]
		card := Card{ID: id, HQ: u.hq, SQ: u.sq}
		if card.HQ == "" {
			card.HQ = card.SQ
			card.SQ = ""
		}
		if seg, ok := segments[id]; ok {
			if m := h3Re.FindStringSubmatch(seg); m != nil {
				if sm := spanRe.FindStringSubmatch(m[1]); sm != nil {
					card.Name = clean(sm[1])
					card.Subtitle = clean(sm[2])
				} else {
					card.Name = clean(m[1])
				}
			}
			if m := selectRe.FindStringSubmatch(seg); m != nil {
				for _, o := range optionRe.FindAllStringSubmatch(m[1], -1) {
					if n, err := strconv.Atoi(o[1]); err == nil && n > 0 {
						card.Seasons = append(card.Seasons, n)
					}
				}
				sort.Ints(card.Seasons)
			}
		}
		card.Track, card.Seasoned = classifyURL(card.HQ)
		if card.Seasoned && len(card.Seasons) == 0 {
			// no select rendered: at least the season in the URL exists
			if m := seasonRe.FindStringSubmatch(pathOf(card.HQ)); m != nil {
				n, _ := strconv.Atoi(m[2])
				card.Seasons = []int{n}
			}
		}
		if !card.Seasoned {
			card.Seasons = nil
		}
		if d.Token == "" {
			d.Token = uuidRe.FindString(card.HQ)
		}
		d.Cards = append(d.Cards, card)
	}

	seen := map[string]bool{}
	for _, m := range anyFeedRe.FindAllString(body, -1) {
		m = html.UnescapeString(m)
		if !seen[m] {
			seen[m] = true
			d.Feeds = append(d.Feeds, m)
		}
	}
	sort.Strings(d.Feeds)
	if d.Token == "" {
		for _, f := range d.Feeds {
			if t := uuidRe.FindString(f); t != "" {
				d.Token = t
				break
			}
		}
	}
	if len(d.Cards) == 0 && len(d.Feeds) == 0 {
		return d, fmt.Errorf("no feed URLs found on the dashboard (page layout may have changed)")
	}
	return d, nil
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

// classifyURL returns the track name embedded in a feed URL and whether the
// URL carries a season segment.
func classifyURL(raw string) (track string, seasoned bool) {
	p := pathOf(raw)
	if m := seasonRe.FindStringSubmatch(p); m != nil {
		return m[1], true
	}
	if m := singleRe.FindStringSubmatch(p); m != nil {
		return m[1], false
	}
	return "", false
}

// Quality classifies a feed URL by the audio quality marker MU embeds in the
// path: "hq" (320 Kbps), "sq" (standard), or "other" (generic/free feeds with
// no marker, e.g. /feed or /feed/podcast).
func Quality(raw string) string {
	p := strings.ToLower(pathOf(raw))
	if p == "" {
		p = strings.ToLower(raw)
	}
	switch {
	case strings.Contains(p, "/hq/") || strings.HasSuffix(p, "/hq") || strings.Contains(p, "muplushq"):
		return "hq"
	case strings.Contains(p, "/sq/") || strings.HasSuffix(p, "/sq") || strings.Contains(p, "muplussq"):
		return "sq"
	case strings.Contains(p, "/feed/inescapable/"):
		return "hq" // single-quality exclusive feed
	default:
		return "other"
	}
}

// FilterQuality selects feeds by quality. quality "all" returns everything;
// "hq"/"sq" return only feeds with that marker (unmarked generic feeds are
// dropped so an HQ request never pulls the free low-quality feed).
func FilterQuality(feeds []string, quality string) []string {
	quality = strings.ToLower(strings.TrimSpace(quality))
	if quality == "" || quality == "all" {
		return feeds
	}
	var out []string
	for _, f := range feeds {
		if Quality(f) == quality {
			out = append(out, f)
		}
	}
	return out
}

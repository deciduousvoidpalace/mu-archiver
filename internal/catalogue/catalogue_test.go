package catalogue

import (
	"os"
	"strings"
	"testing"

	"mu-dl/internal/dashboard"
)

func loadDash(t *testing.T) *dashboard.Dashboard {
	body, err := os.ReadFile("../dashboard/testdata/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	d, err := dashboard.Parse(string(body))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFromDashboardFull(t *testing.T) {
	src := FromDashboard(loadDash(t), Filter{})
	// 35 mu seasons + 34 muplus seasons + inescapable + main mixed feed
	if len(src) != 71 {
		t.Fatalf("sources = %d", len(src))
	}
	if !strings.Contains(src[0].URL, "/feed/mu/2/hq/") || src[0].Season != 2 {
		t.Errorf("first source = %+v", src[0])
	}
	last := src[len(src)-1]
	if last.Track != "muplushq" {
		t.Errorf("mixed feed should be last, got %+v", last)
	}
	for _, s := range src {
		if s.Quality != "hq" || strings.Contains(s.URL, "/sq/") {
			t.Errorf("non-HQ source leaked: %+v", s)
		}
	}
}

func TestFromDashboardFiltered(t *testing.T) {
	src := FromDashboard(loadDash(t), Filter{Quality: "sq", Shows: []string{"inescapable", "muplus"}, MinSeason: 30, MaxSeason: 31})
	var urls []string
	for _, s := range src {
		urls = append(urls, s.URL)
	}
	joined := strings.Join(urls, "\n")
	if strings.Contains(joined, "/feed/mu/") || !strings.Contains(joined, "/feed/muplus/30/sq/") || !strings.Contains(joined, "/feed/muplus/31/sq/") {
		t.Errorf("filter wrong:\n%s", joined)
	}
	if !strings.Contains(joined, "/feed/inescapable/") || !strings.Contains(joined, "/feed/muplussq/") {
		t.Errorf("single feeds missing:\n%s", joined)
	}
	if len(src) != 4 {
		t.Errorf("sources = %d", len(src))
	}
}

func TestReplaceSeason(t *testing.T) {
	got := replaceSeason("https://h/feed/mu/36/hq/tok", 7)
	if got != "https://h/feed/mu/7/hq/tok" {
		t.Errorf("got %q", got)
	}
}

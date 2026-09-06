package dashboard

import (
	"os"
	"reflect"
	"testing"
)

func TestParseRealLayout(t *testing.T) {
	body, err := os.ReadFile("testdata/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(string(body))
	if err != nil {
		t.Fatal(err)
	}
	if d.Plan != "MU Max+ Yearly" {
		t.Errorf("Plan = %q", d.Plan)
	}
	if d.Token != "00000000-0000-4000-8000-000000000000" {
		t.Errorf("Token = %q", d.Token)
	}
	if len(d.Cards) != 4 {
		t.Fatalf("cards = %d: %+v", len(d.Cards), d.Cards)
	}
	byTrack := map[string]Card{}
	for _, c := range d.Cards {
		byTrack[c.Track] = c
	}
	mu := byTrack["mu"]
	if !mu.Seasoned || len(mu.Seasons) != 35 || mu.Seasons[0] != 2 || mu.Seasons[34] != 36 {
		t.Errorf("mu card: %+v", mu)
	}
	if mu.Name != "MU Extended Podcasts" || mu.Subtitle != "All Seasons" {
		t.Errorf("mu heading: %q / %q", mu.Name, mu.Subtitle)
	}
	plus := byTrack["muplus"]
	if !plus.Seasoned || len(plus.Seasons) != 34 || plus.Seasons[0] != 1 || plus.SQ == "" {
		t.Errorf("muplus card: %+v", plus)
	}
	ines := byTrack["inescapable"]
	if ines.Seasoned || ines.SQ != "" || ines.Name != "Inescapable Podcasts" {
		t.Errorf("inescapable card: %+v", ines)
	}
	main := byTrack["muplushq"]
	if main.Seasoned || main.SQ == "" || main.Name != "MU Podcasts & MU Plus+ Podcasts" {
		t.Errorf("main card: %+v", main)
	}
}

func TestQuality(t *testing.T) {
	cases := map[string]string{
		"https://feeds.mysteriousuniverse.org/feed/mu/35/hq/tok":   "hq",
		"https://feeds.mysteriousuniverse.org/feed/muplus/34/sq/t": "sq",
		"https://feeds.mysteriousuniverse.org/feed/muplushq/tok":   "hq",
		"https://feeds.mysteriousuniverse.org/feed/muplussq/tok":   "sq",
		"https://feeds.mysteriousuniverse.org/feed/inescapable/t":  "hq",
		"https://feeds.mysteriousuniverse.org/feed":                "other",
		"https://feeds.mysteriousuniverse.org/feed/podcast":        "other",
	}
	for url, want := range cases {
		if got := Quality(url); got != want {
			t.Errorf("Quality(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestFilterQuality(t *testing.T) {
	feeds := []string{
		"https://h/feed/mu/35/hq/t",
		"https://h/feed/mu/35/sq/t",
		"https://h/feed/muplushq/t",
		"https://h/feed/podcast",
	}
	hq := FilterQuality(feeds, "hq")
	want := []string{"https://h/feed/mu/35/hq/t", "https://h/feed/muplushq/t"}
	if !reflect.DeepEqual(hq, want) {
		t.Errorf("hq filter = %v, want %v", hq, want)
	}
	if all := FilterQuality(feeds, "all"); len(all) != 4 {
		t.Errorf("all filter dropped feeds: %v", all)
	}
}

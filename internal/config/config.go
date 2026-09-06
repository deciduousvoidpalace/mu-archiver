// Package config handles the on-disk configuration and session directory
// (~/.config/mu-dl by default, honouring XDG_CONFIG_HOME). The CLI and the
// GUI share the same file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Config is the persisted user configuration.
type Config struct {
	// BaseURL is the site root, e.g. https://mysteriousuniverse.org
	BaseURL string `json:"base_url"`
	// FeedHost is the host that serves the tokenized feeds.
	FeedHost string `json:"feed_host"`
	// Email/Password are stored only if the user opts in (GUI does by default,
	// the CLI needs --save-credentials). The file is 0600.
	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`
	// Feeds are manually supplied feed URLs (used instead of the dashboard).
	Feeds []string `json:"feeds,omitempty"`
	// Token is the per-user feed token (a UUID), cached from the dashboard.
	Token string `json:"token,omitempty"`
	// OutDir is where episodes are written.
	OutDir string `json:"out_dir,omitempty"`
	// Quality selects which feeds to use: "hq" (default) or "sq".
	Quality string `json:"quality,omitempty"`
	// Layout arranges files: "chronological" (default; one interwoven
	// timeline for all shows) or "seasons" (Show/Season NN/...).
	Layout string `json:"layout,omitempty"`
	// Shows restricts archiving to these show tracks (empty = everything the
	// account can access). Known tracks: mu, muplus, inescapable.
	Shows []string `json:"shows,omitempty"`
	// MinSeason/MaxSeason restrict the season range (0 = no bound).
	MinSeason int `json:"min_season,omitempty"`
	MaxSeason int `json:"max_season,omitempty"`

	// --- politeness / rate limiting -------------------------------------
	// Concurrency is the number of parallel downloads.
	Concurrency int `json:"concurrency,omitempty"`
	// RequestGapMS is the minimum spacing between any two HTTP requests.
	RequestGapMS int `json:"request_gap_ms"`
	// BandwidthKBs caps download throughput in KiB/s (0 = unlimited).
	BandwidthKBs int `json:"bandwidth_kbs"`
	// EpisodeGapSec is the pause each worker takes between files.
	EpisodeGapSec int `json:"episode_gap_sec"`

	// --- background behaviour (GUI) -------------------------------------
	// CheckIntervalHours: re-scan the feeds for new episodes this often when
	// the GUI is running in the background (0 = never).
	CheckIntervalHours int `json:"check_interval_hours"`
	// StartMinimized hides the window to the tray on launch.
	StartMinimized bool `json:"start_minimized"`
	// AutoStart begins archiving as soon as the GUI launches.
	AutoStart bool `json:"auto_start"`
	// Notifications enables desktop notifications.
	Notifications bool `json:"notifications"`
	// CloseToTray hides the window instead of quitting when closed.
	CloseToTray bool `json:"close_to_tray"`
}

// Defaults returns a Config with sensible, polite built-in values.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	out := "MysteriousUniverse"
	if home != "" {
		out = filepath.Join(home, "Podcasts", "Mysterious Universe")
	}
	return Config{
		BaseURL:            "https://mysteriousuniverse.org",
		FeedHost:           "feeds.mysteriousuniverse.org",
		OutDir:             out,
		Quality:            "hq",
		Layout:             "chronological",
		Concurrency:        2,
		RequestGapMS:       1500,
		BandwidthKBs:       0,
		EpisodeGapSec:      5,
		CheckIntervalHours: 6,
		Notifications:      true,
		CloseToTray:        true,
	}
}

// Normalize fills in zero values with defaults and clamps nonsense.
func (c *Config) Normalize() {
	d := Defaults()
	if c.BaseURL == "" {
		c.BaseURL = d.BaseURL
	}
	if c.FeedHost == "" {
		c.FeedHost = d.FeedHost
	}
	if c.OutDir == "" {
		c.OutDir = d.OutDir
	}
	if c.Quality != "sq" {
		c.Quality = "hq"
	}
	if c.Layout != "seasons" {
		c.Layout = "chronological"
	}
	if c.Concurrency <= 0 {
		c.Concurrency = d.Concurrency
	}
	if c.Concurrency > 8 {
		c.Concurrency = 8
	}
	if c.RequestGapMS < 0 {
		c.RequestGapMS = 0
	}
	if c.BandwidthKBs < 0 {
		c.BandwidthKBs = 0
	}
	if c.EpisodeGapSec < 0 {
		c.EpisodeGapSec = 0
	}
	if c.CheckIntervalHours < 0 {
		c.CheckIntervalHours = 0
	}
	sort.Strings(c.Shows)
}

// RequestGap returns the request spacing as a duration.
func (c Config) RequestGap() time.Duration { return time.Duration(c.RequestGapMS) * time.Millisecond }

// EpisodeGap returns the between-files pause as a duration.
func (c Config) EpisodeGap() time.Duration { return time.Duration(c.EpisodeGapSec) * time.Second }

// BandwidthBytes returns the bandwidth cap in bytes/second (0 = unlimited).
func (c Config) BandwidthBytes() int64 { return int64(c.BandwidthKBs) * 1024 }

// Dir returns the configuration/session directory, creating it if needed.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "mu-dl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// CacheDir returns the directory used for cached feed XML.
func CacheDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	cache := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	return cache, nil
}

// Path returns the path to config.json inside the config dir.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// CookiePath returns the path to the persisted session cookies.
func CookiePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cookies.json"), nil
}

// Load reads config.json, falling back to Defaults for any missing file.
// Values present in the file override the defaults; the "gap" fields are
// intentionally not omitempty so a user can set them to 0 explicitly.
func Load() (Config, error) {
	cfg := Defaults()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	// legacy files (v1) lack the gap fields entirely: keep defaults for them
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(data, &probe)
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if _, ok := probe["request_gap_ms"]; !ok {
		cfg.RequestGapMS = Defaults().RequestGapMS
	}
	if _, ok := probe["episode_gap_sec"]; !ok {
		cfg.EpisodeGapSec = Defaults().EpisodeGapSec
	}
	if _, ok := probe["check_interval_hours"]; !ok {
		cfg.CheckIntervalHours = Defaults().CheckIntervalHours
	}
	if _, ok := probe["notifications"]; !ok {
		cfg.Notifications = true
	}
	if _, ok := probe["close_to_tray"]; !ok {
		cfg.CloseToTray = true
	}
	cfg.Normalize()
	return cfg, nil
}

// UpdateToken persists a newly discovered feed token without disturbing any
// other stored setting (callers may be running with per-invocation
// overrides that must not leak into the file).
func UpdateToken(token string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if cfg.Token == token {
		return nil
	}
	cfg.Token = token
	return cfg.Save()
}

// Save writes the config to config.json with restrictive permissions (it may
// contain credentials). The write is atomic (temp file + rename).
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	c.Normalize()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

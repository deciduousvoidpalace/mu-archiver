// Command mu-dl archives the Mysterious Universe podcasts (MU, MU Plus+ and
// Inescapable) that your MU Plus+/Max subscription grants, in the highest
// quality the site offers, from the command line. The desktop app
// (mu-archiver) shares the same engine and configuration.
//
// It uses the same per-user RSS feeds the site hands to any podcast app, so
// it only ever accesses content your subscription already includes. Be a
// good citizen: respect the site's Terms of Service, keep the downloads for
// personal offline use, and leave the rate limits at their polite defaults.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mu-dl/internal/archiver"
	"mu-dl/internal/config"
	"mu-dl/internal/dashboard"
	"mu-dl/internal/util"
)

const version = "2.0.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "login":
		err = cmdLogin(args)
	case "feeds":
		err = cmdFeeds(args)
	case "list", "scan":
		err = cmdList(args)
	case "download", "dl", "archive":
		err = cmdDownload(args, false)
	case "watch":
		err = cmdDownload(args, true)
	case "config":
		err = cmdConfig(args)
	case "version", "--version", "-v":
		fmt.Printf("mu-dl %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`mu-dl - archive your Mysterious Universe podcasts (MU, MU Plus+, Inescapable)

USAGE:
  mu-dl <command> [flags]

COMMANDS:
  login       Check your MU credentials and save the session
  feeds       Show the feeds your subscription grants (from the dashboard)
  list        List every episode in the catalogue and its archive status
  download    Archive everything that is missing (resumable, skips what you have)
  watch       Like download, then re-check for new episodes on an interval
  config      Show or edit stored configuration
  version     Print version

CREDENTIALS:
  --email <addr>          MU account email (or env MU_EMAIL)
  --password <pass>       MU account password (or env MU_PASSWORD)
  --save-credentials      Persist email/password to the config file (0600)

WHAT TO ARCHIVE:
  --out <dir>             Output directory
  --quality hq|sq         Feed quality (default hq = highest the site offers)
  --layout <l>            chronological (default: one timeline, all shows interwoven
                          by date, YYYY/YYYY-MM-DD - SS.EE - Show - Title.mp3)
                          or seasons (Show/Season NN/SS.EE - Title.mp3)
  --shows mu,muplus,inescapable
                          Only these shows (default: all)
  --season <n>            Only this season
  --min-season/--max-season <n>
                          Season range
  --feed <url>            Use a feed URL directly (repeatable); skips login
  --limit <n>             Cap number of downloads (handy for a test run)
  --dry-run               Report what would be downloaded, download nothing
  --overwrite             Re-download files even if already archived

POLITENESS (all adjustable; defaults are deliberately gentle):
  --concurrency <n>       Parallel downloads (default 2)
  --request-gap <dur>     Minimum time between HTTP requests (default 1.5s)
  --episode-gap <dur>     Pause between files per worker (default 5s)
  --bandwidth <KiB/s>     Cap total download speed (default 0 = unlimited)

  --every <dur>           (watch) re-check interval (default 6h)
  --verbose               Show debug output

QUICK START:
  mu-dl login --email you@example.com --save-credentials
  mu-dl download --out ~/Podcasts/MU

Config, session and feed cache live in ~/.config/mu-dl/ (0600).
`)
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

type flags struct {
	cfg        config.Config
	email      string
	password   string
	feeds      multiFlag
	shows      string
	season     int
	limit      int
	dryRun     bool
	overwrite  bool
	saveCreds  bool
	verbose    bool
	requestGap time.Duration
	episodeGap time.Duration
	every      time.Duration
}

func parse(name string, args []string) (*flags, error) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: config: %v (using defaults)\n", err)
	}
	f := &flags{cfg: cfg}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = usage
	fs.StringVar(&f.email, "email", "", "MU account email (or env MU_EMAIL)")
	fs.StringVar(&f.password, "password", "", "MU account password (or env MU_PASSWORD)")
	fs.StringVar(&f.cfg.BaseURL, "base-url", cfg.BaseURL, "site root URL")
	fs.Var(&f.feeds, "feed", "feed URL to use directly (repeatable)")
	fs.StringVar(&f.cfg.OutDir, "out", cfg.OutDir, "output directory")
	fs.StringVar(&f.cfg.Quality, "quality", cfg.Quality, "feed quality: hq or sq")
	fs.StringVar(&f.cfg.Layout, "layout", cfg.Layout, "file layout: chronological (one interwoven timeline) or seasons")
	fs.StringVar(&f.shows, "shows", strings.Join(cfg.Shows, ","), "comma-separated shows: mu,muplus,inescapable")
	fs.IntVar(&f.season, "season", 0, "only this season")
	fs.IntVar(&f.cfg.MinSeason, "min-season", cfg.MinSeason, "lowest season to archive")
	fs.IntVar(&f.cfg.MaxSeason, "max-season", cfg.MaxSeason, "highest season to archive")
	fs.IntVar(&f.cfg.Concurrency, "concurrency", cfg.Concurrency, "parallel downloads")
	fs.DurationVar(&f.requestGap, "request-gap", cfg.RequestGap(), "minimum time between HTTP requests")
	fs.DurationVar(&f.episodeGap, "episode-gap", cfg.EpisodeGap(), "pause between files per worker")
	fs.IntVar(&f.cfg.BandwidthKBs, "bandwidth", cfg.BandwidthKBs, "download cap in KiB/s (0 = unlimited)")
	fs.IntVar(&f.limit, "limit", 0, "cap number of downloads")
	fs.BoolVar(&f.dryRun, "dry-run", false, "don't download, just report")
	fs.BoolVar(&f.overwrite, "overwrite", false, "re-download existing files")
	fs.BoolVar(&f.saveCreds, "save-credentials", false, "persist credentials to config")
	fs.BoolVar(&f.verbose, "verbose", false, "verbose logging")
	fs.DurationVar(&f.every, "every", time.Duration(cfg.CheckIntervalHours)*time.Hour, "(watch) re-check interval")
	// accepted for compatibility with v1 invocations
	fs.Bool("all-seasons", true, "(always on) enumerate every season the account can access")
	fs.Bool("back-catalogue", true, "alias for --all-seasons")
	fs.Bool("save", false, "ignored (feeds are always discovered live)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	f.cfg.RequestGapMS = int(f.requestGap / time.Millisecond)
	f.cfg.EpisodeGapSec = int(f.episodeGap / time.Second)
	f.cfg.Shows = nil
	for _, s := range strings.Split(f.shows, ",") {
		if s = strings.TrimSpace(strings.ToLower(s)); s != "" {
			f.cfg.Shows = append(f.cfg.Shows, s)
		}
	}
	f.cfg.Normalize()
	return f, nil
}

// credentials picks credentials from flags, env, config, or (if allowed) a
// terminal prompt.
func (f *flags) credentials(prompt bool) (string, string, error) {
	email := first(f.email, os.Getenv("MU_EMAIL"), f.cfg.Email)
	pass := first(f.password, os.Getenv("MU_PASSWORD"), f.cfg.Password)
	var err error
	if email == "" && prompt {
		if email, err = util.Prompt("MU email: "); err != nil {
			return "", "", err
		}
	}
	if pass == "" && prompt {
		if pass, err = util.PromptPassword("MU password: "); err != nil {
			return "", "", err
		}
	}
	if email == "" || pass == "" {
		return "", "", fmt.Errorf("email and password required (use --email/--password, env MU_EMAIL/MU_PASSWORD, or run `mu-dl login` interactively)")
	}
	return email, pass, nil
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (f *flags) engine() *archiver.Engine {
	eng := archiver.New(f.cfg)
	eng.Subscribe(func(ev archiver.Event) {
		if ev.Kind == archiver.KindState {
			return
		}
		if ev.Level == archiver.Debug && !f.verbose {
			return
		}
		if ev.Level >= archiver.Warn {
			fmt.Fprintln(os.Stderr, ev.Message)
			return
		}
		fmt.Println(ev.Message)
	})
	return eng
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		fmt.Fprintln(os.Stderr, "\ninterrupted; partial downloads are kept and will resume next time")
		cancel()
	}()
	return ctx, cancel
}

// --- commands -----------------------------------------------------------

func cmdLogin(args []string) error {
	f, err := parse("login", args)
	if err != nil {
		return err
	}
	email, pass, err := f.credentials(true)
	if err != nil {
		return err
	}
	eng := f.engine()
	ctx, cancel := signalContext()
	defer cancel()
	fmt.Printf("Logging in as %s ...\n", email)
	plan, err := eng.TestLogin(ctx, email, pass)
	if err != nil {
		return err
	}
	if plan != "" {
		fmt.Printf("Login successful (%s); session saved.\n", plan)
	} else {
		fmt.Println("Login successful; session saved.")
	}
	if f.saveCreds {
		f.cfg.Email, f.cfg.Password = email, pass
		if err := f.cfg.Save(); err != nil {
			return err
		}
		fmt.Println("Credentials saved to config (0600). Remove with `mu-dl config --clear-credentials`.")
	}
	return nil
}

func cmdFeeds(args []string) error {
	f, err := parse("feeds", args)
	if err != nil {
		return err
	}
	if err := f.applyCredentials(); err != nil {
		return err
	}
	eng := f.engine()
	ctx, cancel := signalContext()
	defer cancel()
	// a scan with an impossible season filter resolves the feed list without
	// reading every feed; simplest is to just scan and print the sources
	_, err = eng.Run(ctx, archiver.RunOptions{ScanOnly: true, Feeds: f.feeds})
	if err != nil {
		return err
	}
	items := eng.Items()
	seen := map[string]int{}
	var order []string
	for _, it := range items {
		if _, ok := seen[it.FeedURL]; !ok {
			order = append(order, it.FeedURL)
		}
		seen[it.FeedURL]++
	}
	fmt.Printf("\n%d feed(s) in use:\n", len(order))
	for _, u := range order {
		fmt.Printf("  [%-2s] %4d episodes  %s\n", strings.ToUpper(dashboard.Quality(u)), seen[u], u)
	}
	return nil
}

func (f *flags) applyCredentials() error {
	if len(f.feeds) > 0 || len(f.cfg.Feeds) > 0 {
		return nil
	}
	email, pass, err := f.credentials(true)
	if err != nil {
		return err
	}
	f.cfg.Email, f.cfg.Password = email, pass
	return nil
}

func cmdList(args []string) error {
	f, err := parse("list", args)
	if err != nil {
		return err
	}
	if err := f.applyCredentials(); err != nil {
		return err
	}
	eng := f.engine()
	ctx, cancel := signalContext()
	defer cancel()
	sum, err := eng.Run(ctx, archiver.RunOptions{ScanOnly: true, Feeds: f.feeds, Season: f.season})
	if err != nil {
		return err
	}
	fmt.Println()
	for _, it := range eng.Items() {
		mark := " "
		switch it.Status {
		case archiver.Archived:
			mark = "✓"
		case archiver.Partial:
			mark = "~"
		}
		date := "          "
		if !it.Published.IsZero() {
			date = it.Published.Format("2006-01-02")
		}
		fmt.Printf("%s %s  %-6s %-17s %s\n", mark, date, it.Number(), it.ShowTag(), it.Title)
	}
	fmt.Printf("\n%d episode(s); %d not yet archived. Output: %s\n", sum.Total, sum.NewFound, f.cfg.OutDir)
	return nil
}

func cmdDownload(args []string, watch bool) error {
	f, err := parse("download", args)
	if err != nil {
		return err
	}
	if err := f.applyCredentials(); err != nil {
		return err
	}
	if f.saveCreds {
		if err := f.cfg.Save(); err != nil {
			return err
		}
	}
	eng := f.engine()
	ctx, cancel := signalContext()
	defer cancel()
	opts := archiver.RunOptions{DryRun: f.dryRun, Overwrite: f.overwrite, Limit: f.limit, Season: f.season, Feeds: f.feeds}
	fmt.Printf("Output: %s   layout=%s quality=%s concurrency=%d request-gap=%s episode-gap=%s bandwidth=%s\n",
		f.cfg.OutDir, f.cfg.Layout, f.cfg.Quality, f.cfg.Concurrency, f.cfg.RequestGap(), f.cfg.EpisodeGap(), bwText(f.cfg.BandwidthKBs))
	for {
		_, err := eng.Run(ctx, opts)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && !watch {
			return err
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
		}
		if !watch {
			return nil
		}
		every := f.every
		if every <= 0 {
			every = 6 * time.Hour
		}
		fmt.Printf("Next check at %s (every %s). Ctrl-C to stop.\n", time.Now().Add(every).Format("15:04"), every)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

func bwText(kb int) string {
	if kb <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d KiB/s", kb)
}

func cmdConfig(args []string) error {
	cfg, _ := config.Load()
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	clearCreds := fs.Bool("clear-credentials", false, "remove stored credentials")
	clearFeeds := fs.Bool("clear-feeds", false, "remove stored feed URLs")
	clearSession := fs.Bool("clear-session", false, "forget the saved login session")
	setOut := fs.String("set-out", "", "set output directory")
	setGap := fs.Duration("set-request-gap", -1, "set minimum time between requests")
	setEpGap := fs.Duration("set-episode-gap", -1, "set pause between files")
	setBW := fs.Int("set-bandwidth", -1, "set bandwidth cap in KiB/s (0 = unlimited)")
	setConc := fs.Int("set-concurrency", 0, "set parallel downloads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	changed := false
	if *clearCreds {
		cfg.Email, cfg.Password = "", ""
		changed = true
	}
	if *clearFeeds {
		cfg.Feeds = nil
		changed = true
	}
	if *clearSession {
		if cp, err := config.CookiePath(); err == nil {
			_ = os.Remove(cp)
		}
		fmt.Println("Session cleared.")
	}
	if *setOut != "" {
		cfg.OutDir = *setOut
		changed = true
	}
	if *setGap >= 0 {
		cfg.RequestGapMS = int(*setGap / time.Millisecond)
		changed = true
	}
	if *setEpGap >= 0 {
		cfg.EpisodeGapSec = int(*setEpGap / time.Second)
		changed = true
	}
	if *setBW >= 0 {
		cfg.BandwidthKBs = *setBW
		changed = true
	}
	if *setConc > 0 {
		cfg.Concurrency = *setConc
		changed = true
	}
	if changed {
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("Config updated.")
	}
	dir, _ := config.Dir()
	fmt.Printf("Config dir    : %s\n", dir)
	fmt.Printf("Site          : %s\n", cfg.BaseURL)
	fmt.Printf("Output        : %s\n", cfg.OutDir)
	fmt.Printf("Quality       : %s\n", cfg.Quality)
	fmt.Printf("Layout        : %s\n", cfg.Layout)
	fmt.Printf("Shows         : %s\n", first(strings.Join(cfg.Shows, ","), "all"))
	fmt.Printf("Concurrency   : %d\n", cfg.Concurrency)
	fmt.Printf("Request gap   : %s\n", cfg.RequestGap())
	fmt.Printf("Episode gap   : %s\n", cfg.EpisodeGap())
	fmt.Printf("Bandwidth cap : %s\n", bwText(cfg.BandwidthKBs))
	fmt.Printf("Credentials   : %s\n", map[bool]string{true: "stored (" + cfg.Email + ")", false: "not stored"}[cfg.Email != ""])
	fmt.Printf("Saved feeds   : %d\n", len(cfg.Feeds))
	return nil
}

// Package archiver is the engine shared by the CLI and the GUI. It owns the
// HTTP session, the rate limiter, the library manifest and the run state
// machine, and drives a run through its phases:
//
//	login -> read dashboard -> fetch feeds -> reconcile with library -> download
//
// Callers observe it through Status snapshots (cheap, poll-friendly) and
// Events (log lines and notable moments).
package archiver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"mu-dl/internal/auth"
	"mu-dl/internal/catalogue"
	"mu-dl/internal/config"
	"mu-dl/internal/dashboard"
	"mu-dl/internal/downloader"
	"mu-dl/internal/feed"
	"mu-dl/internal/httpx"
	"mu-dl/internal/library"
	"mu-dl/internal/ratelimit"
	"mu-dl/internal/util"
)

// State is the engine's coarse run state.
type State string

// Engine states.
const (
	StateIdle        State = "idle"
	StateLoggingIn   State = "logging in"
	StateScanning    State = "scanning"
	StateDownloading State = "archiving"
	StatePaused      State = "paused"
	StateCoolingDown State = "cooling down"
	StateStopping    State = "stopping"
	StateDone        State = "done"
	StateFailed      State = "failed"
)

// Level is an event severity.
type Level int

// Event levels.
const (
	Debug Level = iota
	Info
	Warn
	Error
)

// Kind classifies events beyond plain log lines.
type Kind string

// Event kinds.
const (
	KindLog       Kind = "log"
	KindState     Kind = "state"
	KindScanDone  Kind = "scan-done"
	KindEpisode   Kind = "episode"
	KindRunDone   Kind = "run-done"
	KindRunFail   Kind = "run-failed"
	KindCooldown  Kind = "cooldown"
	KindLoginOK   Kind = "login-ok"
	KindLoginFail Kind = "login-failed"
)

// Event is emitted to subscribers.
type Event struct {
	Time    time.Time
	Kind    Kind
	Level   Level
	Message string
	Episode *feed.Episode
	Summary *Summary
}

// Summary describes a finished run.
type Summary struct {
	Total      int
	Downloaded int
	Skipped    int
	Failed     int
	Bytes      int64
	Duration   time.Duration
	NewFound   int // episodes discovered that were not yet archived
	ScanOnly   bool
	DryRun     bool
}

// ItemStatus is what the library knows about a catalogue episode.
type ItemStatus string

// Item statuses.
const (
	Archived ItemStatus = "archived"
	Pending  ItemStatus = "pending"
	Partial  ItemStatus = "partial"
	Failed   ItemStatus = "failed"
	Active   ItemStatus = "downloading"
)

// Item is a catalogue episode with its archive status.
type Item struct {
	feed.Episode
	Status ItemStatus
	Path   string // absolute destination
	Size   int64  // bytes on disk (archived) or expected (pending)
	Error  string
}

// Status is a point-in-time snapshot for UIs.
type Status struct {
	State         State
	Message       string
	Email         string
	Plan          string
	LoggedIn      bool
	OutDir        string
	Started       time.Time
	CooldownUntil time.Time
	LastScan      time.Time
	NextScan      time.Time

	Sources     int
	SourcesDone int

	Total      int // episodes in catalogue
	Archived   int // complete on disk
	Queued     int // pending in this run
	Downloaded int // finished this run
	Failed     int
	BytesRun   int64
	BytesTotal int64 // library size on disk
	Speed      float64

	Transfers []downloader.Progress
	Limits    ratelimit.Limits
}

// RunOptions tune one run.
type RunOptions struct {
	ScanOnly  bool
	DryRun    bool
	Overwrite bool
	Limit     int
	Season    int      // only this season (0 = all)
	Feeds     []string // explicit feed URLs (skip login/dashboard)
	Email     string   // override credentials for this run
	Password  string
}

// Engine is safe for concurrent use.
type Engine struct {
	mu      sync.Mutex
	cfg     config.Config
	limiter *ratelimit.Limiter
	client  *httpx.Client

	state                State
	message              string
	plan                 string
	email                string
	loggedIn             bool
	started              time.Time
	lastScan             time.Time
	nextScan             time.Time
	sources, sourcesDone int
	stats                struct {
		total, archived, queued, downloaded, failed int
		bytesRun                                    int64
	}
	items     []Item
	itemIdx   map[string]int
	transfers map[*downloader.Transfer]struct{}
	lib       *library.Library

	running bool
	cancel  context.CancelFunc
	runDone chan struct{}

	subs   map[int]func(Event)
	nextID int
	log    []Event

	watchCancel context.CancelFunc
}

// New creates an engine with the given configuration.
func New(cfg config.Config) *Engine {
	cfg.Normalize()
	e := &Engine{
		cfg:       cfg,
		state:     StateIdle,
		subs:      map[int]func(Event){},
		itemIdx:   map[string]int{},
		transfers: map[*downloader.Transfer]struct{}{},
	}
	e.limiter = ratelimit.New(limitsFrom(cfg))
	e.email = cfg.Email
	return e
}

func limitsFrom(cfg config.Config) ratelimit.Limits {
	return ratelimit.Limits{RequestGap: cfg.RequestGap(), Bandwidth: cfg.BandwidthBytes(), EpisodeGap: cfg.EpisodeGap()}
}

// Config returns the current configuration.
func (e *Engine) Config() config.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

// SetConfig replaces the configuration. Rate limits take effect immediately,
// even mid-run; everything else applies to the next run.
func (e *Engine) SetConfig(cfg config.Config) {
	cfg.Normalize()
	e.mu.Lock()
	e.cfg = cfg
	if cfg.Email != "" {
		e.email = cfg.Email
	}
	e.mu.Unlock()
	e.limiter.Set(limitsFrom(cfg))
}

// Limiter exposes the rate limiter (for live adjustment).
func (e *Engine) Limiter() *ratelimit.Limiter { return e.limiter }

// Subscribe registers an event callback and returns an unsubscribe func.
// Callbacks are invoked synchronously; keep them quick.
func (e *Engine) Subscribe(fn func(Event)) func() {
	e.mu.Lock()
	id := e.nextID
	e.nextID++
	e.subs[id] = fn
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
	}
}

// Log returns recent log events (most recent last).
func (e *Engine) Log() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.log...)
}

func (e *Engine) emit(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.Kind == "" {
		ev.Kind = KindLog
	}
	e.mu.Lock()
	if ev.Kind == KindLog || ev.Message != "" {
		e.log = append(e.log, ev)
		if len(e.log) > 2000 {
			e.log = e.log[len(e.log)-2000:]
		}
	}
	subs := make([]func(Event), 0, len(e.subs))
	for _, fn := range e.subs {
		subs = append(subs, fn)
	}
	e.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
}

func (e *Engine) logf(level Level, format string, args ...any) {
	e.emit(Event{Level: level, Message: fmt.Sprintf(format, args...)})
}

func (e *Engine) setState(s State, msg string) {
	e.mu.Lock()
	e.state = s
	e.message = msg
	e.mu.Unlock()
	e.emit(Event{Kind: KindState, Level: Debug, Message: msg})
}

// Status returns a snapshot for display.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Status{
		State: e.state, Message: e.message, Email: e.email, Plan: e.plan, LoggedIn: e.loggedIn,
		OutDir: e.cfg.OutDir, Started: e.started, LastScan: e.lastScan, NextScan: e.nextScan,
		Sources: e.sources, SourcesDone: e.sourcesDone,
		Total: e.stats.total, Archived: e.stats.archived, Queued: e.stats.queued,
		Downloaded: e.stats.downloaded, Failed: e.stats.failed, BytesRun: e.stats.bytesRun,
		Limits: e.limiter.Limits(),
	}
	if e.lib != nil {
		st.BytesTotal = e.lib.TotalBytes()
	}
	st.CooldownUntil = e.limiter.CooldownUntil()
	if e.running {
		switch {
		case e.limiter.Paused():
			st.State = StatePaused
		case !st.CooldownUntil.IsZero():
			st.State = StateCoolingDown
		}
	}
	for tr := range e.transfers {
		p := tr.Snapshot()
		st.Transfers = append(st.Transfers, p)
		st.Speed += p.Speed
	}
	sort.Slice(st.Transfers, func(i, j int) bool { return st.Transfers[i].Started.Before(st.Transfers[j].Started) })
	return st
}

// Items returns the catalogue with statuses (from the last scan).
func (e *Engine) Items() []Item {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Item(nil), e.items...)
}

// Running reports whether a run is in progress.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Pause suspends all network activity (takes effect mid-transfer).
func (e *Engine) Pause() {
	e.limiter.Pause()
	e.emit(Event{Kind: KindState, Level: Info, Message: "Paused"})
}

// Resume lifts a pause.
func (e *Engine) Resume() {
	e.limiter.Resume()
	e.emit(Event{Kind: KindState, Level: Info, Message: "Resumed"})
}

// Paused reports whether the engine is paused.
func (e *Engine) Paused() bool { return e.limiter.Paused() }

// SkipCooldown cancels a server-imposed cool-down early (use with care).
func (e *Engine) SkipCooldown() { e.limiter.ClearCooldown() }

// Stop cancels the current run (partial files are kept for resume).
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	running := e.running
	e.mu.Unlock()
	if running && cancel != nil {
		e.setState(StateStopping, "Stopping…")
		e.limiter.Resume() // unblock paused workers so they can observe the cancel
		cancel()
	}
}

// Start launches a run in the background. It fails if one is in progress.
func (e *Engine) Start(opts RunOptions) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return errors.New("a run is already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.running = true
	e.runDone = make(chan struct{})
	done := e.runDone
	e.mu.Unlock()
	go func() {
		defer close(done)
		_ = e.run(ctx, opts)
	}()
	return nil
}

// Wait blocks until the current background run finishes.
func (e *Engine) Wait() {
	e.mu.Lock()
	done := e.runDone
	e.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Run executes a run synchronously (CLI).
func (e *Engine) Run(ctx context.Context, opts RunOptions) (*Summary, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil, errors.New("a run is already in progress")
	}
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.running = true
	e.runDone = make(chan struct{})
	done := e.runDone
	e.mu.Unlock()
	defer close(done)
	return e.runSummary(ctx, opts)
}

func (e *Engine) run(ctx context.Context, opts RunOptions) error {
	_, err := e.runSummary(ctx, opts)
	return err
}

// --- the run itself -----------------------------------------------------

func (e *Engine) runSummary(ctx context.Context, opts RunOptions) (sum *Summary, err error) {
	start := time.Now()
	e.mu.Lock()
	cfg := e.cfg
	e.started = start
	e.stats.queued, e.stats.downloaded, e.stats.failed, e.stats.bytesRun = 0, 0, 0, 0
	e.sources, e.sourcesDone = 0, 0
	e.mu.Unlock()
	e.limiter.Resume()
	e.limiter.ClearCooldown()

	defer func() {
		e.mu.Lock()
		e.running = false
		e.cancel = nil
		for tr := range e.transfers {
			delete(e.transfers, tr)
		}
		e.mu.Unlock()
		if e.lib != nil {
			_ = e.lib.Save()
		}
		switch {
		case err != nil && errors.Is(err, context.Canceled):
			e.setState(StateIdle, "Stopped")
		case err != nil:
			e.setState(StateFailed, err.Error())
			e.logf(Error, "Run failed: %v", err)
			e.emit(Event{Kind: KindRunFail, Level: Error, Message: err.Error()})
		default:
			e.setState(StateDone, "Up to date")
		}
		if sum != nil {
			sum.Duration = time.Since(start)
			e.emit(Event{Kind: KindRunDone, Level: Info, Summary: sum, Message: summaryLine(sum)})
		}
	}()

	// 1. library
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create output directory %s: %w", cfg.OutDir, err)
	}
	lib, err := library.Open(cfg.OutDir, feed.ParseLayout(cfg.Layout))
	if err != nil {
		return nil, fmt.Errorf("opening library manifest: %w", err)
	}
	e.mu.Lock()
	e.lib = lib
	e.mu.Unlock()

	// 2. sources
	client, err := e.newClient(true)
	if err != nil {
		return nil, err
	}
	hasCreds := cfg.Email != "" && cfg.Password != ""
	fromDashboard := false
	var sources []catalogue.Source
	switch {
	case len(opts.Feeds) > 0:
		// Explicit --feed URLs win and need no login.
		for _, f := range opts.Feeds {
			track, _ := classifyTrack(f)
			sources = append(sources, catalogue.Source{URL: f, Track: track, Show: showLabel(track), Quality: dashboard.Quality(f)})
		}
		e.logf(Info, "Using %d feed URL(s) supplied directly (no login needed)", len(sources))
	case hasCreds:
		// A configured account: enumerate the whole subscription from the
		// dashboard. Saved cfg.Feeds (a v1 leftover) is deliberately ignored
		// here so it can neither pin the run to a subset nor empty it.
		if len(cfg.Feeds) > 0 {
			e.logf(Debug, "Ignoring %d saved feed URL(s); using the dashboard because an account is configured", len(cfg.Feeds))
		}
		fromDashboard = true
		sources, err = e.resolveFromDashboard(ctx, client, cfg, opts)
		if err != nil {
			return nil, err
		}
	case len(cfg.Feeds) > 0:
		// No credentials, but the user pasted feed URLs: honour them.
		for _, f := range dashboard.FilterQuality(cfg.Feeds, cfg.Quality) {
			track, _ := classifyTrack(f)
			sources = append(sources, catalogue.Source{URL: f, Track: track, Show: showLabel(track), Quality: dashboard.Quality(f)})
		}
		e.logf(Info, "Using %d saved feed URL(s) from config", len(sources))
		if len(sources) == 0 {
			return nil, fmt.Errorf("your %d saved feed URL(s) are all %s-quality but the selected quality is %s; change the quality or clear the saved feeds", len(cfg.Feeds), otherQuality(cfg.Quality), strings.ToUpper(cfg.Quality))
		}
	default:
		fromDashboard = true
		sources, err = e.resolveFromDashboard(ctx, client, cfg, opts)
		if err != nil {
			return nil, err
		}
	}
	if len(sources) == 0 {
		return nil, noSourcesError(cfg, opts, fromDashboard)
	}

	// 3. scan feeds
	e.setState(StateScanning, fmt.Sprintf("Reading %d feeds…", len(sources)))
	e.mu.Lock()
	e.sources = len(sources)
	e.mu.Unlock()
	cacheDir, _ := config.CacheDir()
	cache := newFileCache(cacheDir)
	var lists [][]feed.Episode
	failedFeeds := 0
	for i, s := range sources {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		e.mu.Lock()
		e.message = fmt.Sprintf("Reading %s (%d/%d)", s.Label(), i+1, len(sources))
		e.mu.Unlock()
		eps, fromCache, ferr := feed.Fetch(ctx, client, s.URL, s.Track, cache)
		if ferr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failedFeeds++
			e.logf(Warn, "Skipping %s: %v", s.Label(), ferr)
		} else {
			src := ""
			if fromCache {
				src = " (cached)"
			}
			e.logf(Debug, "%s: %d episodes%s", s.Label(), len(eps), src)
			lists = append(lists, eps)
		}
		e.mu.Lock()
		e.sourcesDone = i + 1
		e.mu.Unlock()
	}
	if len(lists) == 0 {
		return nil, fmt.Errorf("none of the %d feeds could be read", len(sources))
	}
	eps := feed.Dedup(lists...)
	// The feed list was already narrowed, but the mixed main feed carries every
	// show and season, so the show/season filters are applied per episode too.
	minS, maxS := cfg.MinSeason, cfg.MaxSeason
	if opts.Season > 0 {
		minS, maxS = opts.Season, opts.Season
	}
	allowed := allowedShows(cfg.Shows)
	var filtered []feed.Episode
	for _, ep := range eps {
		if minS > 0 && ep.Season < minS || maxS > 0 && ep.Season > maxS {
			continue
		}
		if allowed != nil && !allowed[ep.Show] {
			continue
		}
		filtered = append(filtered, ep)
	}
	eps = filtered
	feed.Sort(eps)

	// 4. reconcile with library
	items := make([]Item, 0, len(eps))
	idx := map[string]int{}
	archived, newFound := 0, 0
	var queue []int
	for _, ep := range eps {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		it := Item{Episode: ep}
		st, dst := lib.Check(ep)
		it.Path = dst
		switch {
		case st == library.Complete && !opts.Overwrite:
			it.Status = Archived
			if en, ok := lib.Get(ep); ok {
				it.Size = en.Size
			}
			archived++
		case st == library.Partial:
			it.Status = Partial
			it.Size = ep.Length
			newFound++
		default:
			it.Status = Pending
			it.Size = ep.Length
			newFound++
		}
		if it.Status != Archived {
			if opts.Limit <= 0 || len(queue) < opts.Limit {
				queue = append(queue, len(items))
			}
		}
		idx[ep.Key()] = len(items)
		items = append(items, it)
	}
	_ = lib.Save()
	e.mu.Lock()
	e.items, e.itemIdx = items, idx
	e.stats.total, e.stats.archived, e.stats.queued = len(items), archived, len(queue)
	e.lastScan = time.Now()
	e.mu.Unlock()
	msg := fmt.Sprintf("Catalogue: %d episodes across %d feeds — %d archived, %d to fetch", len(items), len(lists), archived, newFound)
	if failedFeeds > 0 {
		msg += fmt.Sprintf(" (%d feeds unreadable)", failedFeeds)
	}
	e.emit(Event{Kind: KindScanDone, Level: Info, Message: msg, Summary: &Summary{Total: len(items), NewFound: newFound}})

	sum = &Summary{Total: len(items), NewFound: newFound, Skipped: archived, ScanOnly: opts.ScanOnly, DryRun: opts.DryRun}
	if opts.ScanOnly {
		return sum, nil
	}
	if opts.DryRun {
		for _, i := range queue {
			e.logf(Info, "DRY-RUN would download %s -> %s", items[i].EnclosureURL, items[i].Path)
		}
		sum.Skipped = len(queue)
		return sum, nil
	}
	if len(queue) == 0 {
		e.logf(Info, "Nothing to do: every episode is already archived.")
		return sum, nil
	}

	// 5. download
	e.setState(StateDownloading, fmt.Sprintf("Archiving %d episodes…", len(queue)))
	workers := cfg.Concurrency
	if workers <= 0 {
		workers = 1
	}
	if workers > len(queue) {
		workers = len(queue)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				e.downloadItem(ctx, client, lib, i, opts.Overwrite)
				if ctx.Err() == nil {
					_ = e.limiter.WaitEpisodeGap(ctx)
				}
			}
		}()
	}
	for _, i := range queue {
		select {
		case jobs <- i:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return sum, ctx.Err()
	}
	e.mu.Lock()
	sum.Downloaded, sum.Failed, sum.Bytes = e.stats.downloaded, e.stats.failed, e.stats.bytesRun
	e.mu.Unlock()
	if sum.Failed > 0 {
		return sum, fmt.Errorf("%d download(s) failed", sum.Failed)
	}
	return sum, nil
}

func summaryLine(s *Summary) string {
	switch {
	case s.ScanOnly:
		return fmt.Sprintf("Scan complete: %d episodes, %d not yet archived", s.Total, s.NewFound)
	case s.DryRun:
		return fmt.Sprintf("Dry run: %d episodes would be downloaded", s.Skipped)
	}
	return fmt.Sprintf("Done in %s: %d downloaded, %d already archived, %d failed, %s transferred",
		s.Duration.Round(time.Second), s.Downloaded, s.Skipped, s.Failed, util.HumanBytes(s.Bytes))
}

func (e *Engine) downloadItem(ctx context.Context, client *httpx.Client, lib *library.Library, i int, overwrite bool) {
	e.mu.Lock()
	it := e.items[i]
	e.items[i].Status = Active
	e.mu.Unlock()

	if overwrite {
		lib.Forget(it.Episode)
		_ = os.Remove(it.Path)
	}
	var tr *downloader.Transfer
	size, err := downloader.Download(ctx, client, it.EnclosureURL, it.Path, downloader.Options{
		OnProgress: func(t *downloader.Transfer) {
			tr = t
			e.mu.Lock()
			e.transfers[t] = struct{}{}
			e.mu.Unlock()
		},
	})
	e.mu.Lock()
	if tr != nil {
		delete(e.transfers, tr)
	}
	e.mu.Unlock()

	if err != nil {
		if ctx.Err() != nil {
			e.mu.Lock()
			e.items[i].Status = Partial
			e.mu.Unlock()
			return
		}
		e.mu.Lock()
		e.items[i].Status = Failed
		e.items[i].Error = err.Error()
		e.stats.failed++
		e.mu.Unlock()
		e.emit(Event{Kind: KindEpisode, Level: Error, Episode: &it.Episode, Message: fmt.Sprintf("FAIL  %s: %v", it.FileName(), err)})
		return
	}
	lib.Record(it.Episode, lib.PathFor(it.Episode), size)
	_ = lib.Save()
	e.mu.Lock()
	e.items[i].Status = Archived
	e.items[i].Size = size
	e.stats.downloaded++
	e.stats.archived++
	e.stats.bytesRun += size
	e.mu.Unlock()
	e.emit(Event{Kind: KindEpisode, Level: Info, Episode: &it.Episode, Message: fmt.Sprintf("OK    %s (%s)", it.FileName(), util.HumanBytes(size))})
}

// --- session -------------------------------------------------------------

func (e *Engine) newClient(follow bool) (*httpx.Client, error) {
	c, err := httpx.New(follow, 10*time.Minute, e.limiter)
	if err != nil {
		return nil, err
	}
	c.OnPushback = func(status int, wait time.Duration, u string) {
		e.emit(Event{Kind: KindCooldown, Level: Warn, Message: fmt.Sprintf("Server answered HTTP %d for %s — cooling down for %s", status, hostOf(u), wait.Round(time.Second))})
	}
	if cp, err := config.CookiePath(); err == nil {
		_ = c.LoadCookies(cp)
	}
	e.mu.Lock()
	c.SetPersistHosts(hostOf(e.cfg.BaseURL), e.cfg.FeedHost)
	e.mu.Unlock()
	return c, nil
}

func (e *Engine) saveSession(c *httpx.Client) {
	if cp, err := config.CookiePath(); err == nil {
		_ = c.SaveCookies(cp)
	}
}

// ensureLogin makes sure client carries an authenticated session, logging in
// with the configured credentials if needed. It returns the dashboard HTML.
func (e *Engine) ensureLogin(ctx context.Context, client *httpx.Client, cfg config.Config, email, password string) (string, error) {
	if body, ok := auth.VerifySession(ctx, client, cfg.BaseURL); ok {
		e.mu.Lock()
		e.loggedIn = true
		e.mu.Unlock()
		e.logf(Debug, "Existing session is still valid")
		return body, nil
	}
	if email == "" || password == "" {
		return "", errors.New("not logged in and no credentials configured")
	}
	e.setState(StateLoggingIn, "Logging in as "+email)
	lc, err := httpx.New(false, 2*time.Minute, e.limiter)
	if err != nil {
		return "", err
	}
	lc.ShareJar(client)
	lc.SetPersistHosts(hostOf(cfg.BaseURL), cfg.FeedHost)
	if err := auth.Login(ctx, lc, cfg.BaseURL, email, password); err != nil {
		e.mu.Lock()
		e.loggedIn = false
		e.mu.Unlock()
		e.emit(Event{Kind: KindLoginFail, Level: Error, Message: "Login failed: " + err.Error()})
		return "", fmt.Errorf("login failed: %w", err)
	}
	body, ok := auth.VerifySession(ctx, client, cfg.BaseURL)
	if !ok {
		return "", errors.New("login appeared to succeed but the dashboard is still not accessible")
	}
	e.mu.Lock()
	e.loggedIn = true
	e.email = email
	e.mu.Unlock()
	e.saveSession(client)
	e.emit(Event{Kind: KindLoginOK, Level: Info, Message: "Logged in as " + email})
	return body, nil
}

func (e *Engine) resolveFromDashboard(ctx context.Context, client *httpx.Client, cfg config.Config, opts RunOptions) ([]catalogue.Source, error) {
	email, password := cfg.Email, cfg.Password
	if opts.Email != "" {
		email, password = opts.Email, opts.Password
	}
	body, err := e.ensureLogin(ctx, client, cfg, email, password)
	if err != nil {
		return nil, err
	}
	dash, err := dashboard.Parse(body)
	if err != nil {
		if dash == nil {
			return nil, err
		}
		e.logf(Warn, "Dashboard: %v", err)
	}
	e.mu.Lock()
	if dash.Plan != "" {
		e.plan = dash.Plan
	}
	if dash.Token != "" && e.cfg.Token != dash.Token {
		e.cfg.Token = dash.Token
		cfg.Token = dash.Token
		_ = config.UpdateToken(dash.Token)
	}
	e.mu.Unlock()
	if dash.Plan != "" {
		e.logf(Info, "Subscription: %s", dash.Plan)
	}
	filter := catalogue.Filter{Quality: cfg.Quality, Shows: cfg.Shows, MinSeason: cfg.MinSeason, MaxSeason: cfg.MaxSeason}
	if opts.Season > 0 {
		filter.MinSeason, filter.MaxSeason = opts.Season, opts.Season
	}
	sources := catalogue.FromDashboard(dash, filter)
	for _, c := range dash.Cards {
		if c.Seasoned && len(c.Seasons) > 0 {
			e.logf(Info, "%s: seasons %d–%d available", catalogue.ShowName(c.Track), c.Seasons[0], c.Seasons[len(c.Seasons)-1])
		} else if c.Track != "" {
			e.logf(Info, "%s: single feed", catalogue.ShowName(c.Track))
		}
	}
	if len(sources) == 0 && dash.Token != "" {
		e.logf(Warn, "Dashboard layout not recognised; probing season feeds directly")
		sources = catalogue.Probe(ctx, client, cfg.FeedHost, dash.Token, catalogue.KnownTracks[:2], filter, 3, func(s catalogue.Source, ok bool) {
			if ok {
				e.logf(Debug, "found %s", s.Label())
			}
		})
	}
	e.saveSession(client)
	return sources, nil
}

// TestLogin verifies credentials without starting a run and returns the
// subscription plan seen on the dashboard.
func (e *Engine) TestLogin(ctx context.Context, email, password string) (string, error) {
	e.mu.Lock()
	cfg := e.cfg
	e.mu.Unlock()
	client, err := e.newClient(true)
	if err != nil {
		return "", err
	}
	// force a fresh login so a stale cookie can't mask bad credentials
	if cp, err := config.CookiePath(); err == nil {
		client.ClearCookies(cp)
	}
	body, err := e.ensureLogin(ctx, client, cfg, email, password)
	if err != nil {
		return "", err
	}
	dash, _ := dashboard.Parse(body)
	plan := ""
	if dash != nil {
		plan = dash.Plan
		e.mu.Lock()
		e.plan = plan
		e.mu.Unlock()
	}
	e.mu.Lock()
	e.state = StateIdle
	e.mu.Unlock()
	return plan, nil
}

// Logout forgets the stored session cookie.
func (e *Engine) Logout() {
	if cp, err := config.CookiePath(); err == nil {
		_ = os.Remove(cp)
	}
	e.mu.Lock()
	e.loggedIn = false
	e.plan = ""
	e.mu.Unlock()
}

// --- background schedule -------------------------------------------------

// StartWatch runs a full archive pass every interval until StopWatch is
// called; with runNow the first pass starts immediately, otherwise after one
// interval. Passes that overlap a manual run are skipped.
func (e *Engine) StartWatch(interval time.Duration, runNow bool) {
	e.StopWatch()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.watchCancel = cancel
	if runNow {
		e.nextScan = time.Now()
	} else {
		e.nextScan = time.Now().Add(interval)
	}
	e.mu.Unlock()
	go func() {
		first := true
		for {
			if !first || runNow {
				if !e.Running() {
					_ = e.Start(RunOptions{})
					e.Wait()
				}
				e.mu.Lock()
				e.nextScan = time.Now().Add(interval)
				e.mu.Unlock()
			}
			first = false
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

// StopWatch cancels the background schedule (a run in progress continues).
func (e *Engine) StopWatch() {
	e.mu.Lock()
	cancel := e.watchCancel
	e.watchCancel = nil
	e.nextScan = time.Time{}
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Watching reports whether a background schedule is active.
func (e *Engine) Watching() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.watchCancel != nil
}

// --- helpers -------------------------------------------------------------

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
		if i := strings.IndexByte(s, '/'); i >= 0 {
			s = s[:i]
		}
		return s
	}
	return u.Host
}

func classifyTrack(raw string) (string, bool) {
	p := strings.ToLower(raw)
	switch {
	case strings.Contains(p, "/feed/muplus/"):
		return "muplus", true
	case strings.Contains(p, "/feed/mu/"):
		return "mu", true
	case strings.Contains(p, "/feed/inescapable/"):
		return "inescapable", false
	}
	return "", false
}

// allowedShows maps track names to show folder names; nil means everything.
func allowedShows(tracks []string) map[string]bool {
	if len(tracks) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, t := range tracks {
		switch t {
		case "mu":
			m[feed.ShowMU] = true
		case "muplus":
			m[feed.ShowPlus] = true
		case "inescapable":
			m[feed.ShowInescapable] = true
		}
	}
	return m
}

func showLabel(track string) string {
	if track == "" {
		return "Feed"
	}
	return catalogue.ShowName(track)
}

func otherQuality(q string) string {
	if strings.EqualFold(q, "sq") {
		return "HQ"
	}
	return "SQ"
}

// noSourcesError explains, in plain terms, why a logged-in run found nothing
// to archive, naming the active show/season filter instead of guessing.
func noSourcesError(cfg config.Config, opts RunOptions, fromDashboard bool) error {
	if !fromDashboard {
		return errors.New("no feeds to archive")
	}
	var b strings.Builder
	b.WriteString("no episodes matched your filters")
	if len(cfg.Shows) > 0 {
		names := make([]string, 0, len(cfg.Shows))
		for _, s := range cfg.Shows {
			names = append(names, catalogue.ShowName(s))
		}
		fmt.Fprintf(&b, "; shows: %s", strings.Join(names, ", "))
	} else {
		b.WriteString("; shows: all")
	}
	min, max := cfg.MinSeason, cfg.MaxSeason
	if opts.Season > 0 {
		min, max = opts.Season, opts.Season
	}
	switch {
	case min > 0 && max > 0:
		fmt.Fprintf(&b, "; seasons %d–%d", min, max)
	case min > 0:
		fmt.Fprintf(&b, "; seasons %d and up", min)
	case max > 0:
		fmt.Fprintf(&b, "; seasons up to %d", max)
	}
	b.WriteString(". Widen the show or season range in Settings.")
	return errors.New(b.String())
}

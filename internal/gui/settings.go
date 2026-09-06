package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"mu-dl/internal/assets"
	"mu-dl/internal/config"
)

type settingsPage struct {
	ui  *App
	box fyne.CanvasObject

	email, password                                                 *widget.Entry
	loginStatus                                                     *widget.Label
	outDir                                                          *widget.Entry
	quality                                                         *widget.RadioGroup
	layout                                                          *widget.RadioGroup
	showMU, showPlus, showIne                                       *widget.Check
	minSeason, maxSeason                                            *widget.Entry
	interval                                                        *widget.Select
	startMin, autoStart, closeToTray, notifications, autostartLogin *widget.Check
	concurrency, gap, bw, epGap                                     *widget.Entry
	saved                                                           *widget.Label
}

var intervalOptions = []string{"Off", "Every hour", "Every 3 hours", "Every 6 hours", "Every 12 hours", "Daily"}

func intervalHours(s string) int {
	switch s {
	case "Every hour":
		return 1
	case "Every 3 hours":
		return 3
	case "Every 6 hours":
		return 6
	case "Every 12 hours":
		return 12
	case "Daily":
		return 24
	}
	return 0
}

func intervalLabel(h int) string {
	for _, o := range intervalOptions {
		if intervalHours(o) == h {
			return o
		}
	}
	if h > 0 {
		return "Every 6 hours"
	}
	return "Off"
}

func newSettingsPage(ui *App) *settingsPage {
	p := &settingsPage{ui: ui}
	cfg := ui.cfg

	// account
	p.email = widget.NewEntry()
	p.email.SetPlaceHolder("you@example.com")
	p.email.SetText(cfg.Email)
	p.password = widget.NewPasswordEntry()
	p.password.SetText(cfg.Password)
	p.loginStatus = widget.NewLabel("")
	p.loginStatus.Wrapping = fyne.TextWrapWord
	testBtn := widget.NewButton("Test login", p.testLogin)
	forget := widget.NewButton("Forget session", func() {
		ui.eng.Logout()
		p.loginStatus.SetText("Saved session cleared; the next run will log in again.")
	})
	account := card("ACCOUNT", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Email", p.email),
			widget.NewFormItem("Password", p.password),
		),
		container.NewHBox(testBtn, forget),
		p.loginStatus,
		hint("Credentials are stored in ~/.config/mu-dl/config.json (readable only by you)."),
	))

	// archive
	p.outDir = widget.NewEntry()
	p.outDir.SetText(cfg.OutDir)
	browse := widget.NewButton("Browse…", p.browse)
	p.quality = widget.NewRadioGroup([]string{"HQ (highest available, 320 kbps for recent seasons)", "SQ (smaller files)"}, nil)
	p.quality.Horizontal = false
	if cfg.Quality == "sq" {
		p.quality.SetSelected(p.quality.Options[1])
	} else {
		p.quality.SetSelected(p.quality.Options[0])
	}
	p.layout = widget.NewRadioGroup([]string{
		"One timeline — all shows interwoven by release date",
		"By show and season",
	}, nil)
	if cfg.Layout == "seasons" {
		p.layout.SetSelected(p.layout.Options[1])
	} else {
		p.layout.SetSelected(p.layout.Options[0])
	}
	p.showMU = widget.NewCheck("Mysterious Universe", nil)
	p.showPlus = widget.NewCheck("MU Plus+", nil)
	p.showIne = widget.NewCheck("Inescapable", nil)
	p.showMU.SetChecked(len(cfg.Shows) == 0 || has(cfg.Shows, "mu"))
	p.showPlus.SetChecked(len(cfg.Shows) == 0 || has(cfg.Shows, "muplus"))
	p.showIne.SetChecked(len(cfg.Shows) == 0 || has(cfg.Shows, "inescapable"))
	p.minSeason = widget.NewEntry()
	p.minSeason.SetPlaceHolder("first")
	p.maxSeason = widget.NewEntry()
	p.maxSeason.SetPlaceHolder("latest")
	if cfg.MinSeason > 0 {
		p.minSeason.SetText(strconv.Itoa(cfg.MinSeason))
	}
	if cfg.MaxSeason > 0 {
		p.maxSeason.SetText(strconv.Itoa(cfg.MaxSeason))
	}
	seasons := container.NewGridWithColumns(4, widget.NewLabel("Seasons from"), p.minSeason, widget.NewLabel("to"), p.maxSeason)
	archive := card("WHAT TO ARCHIVE", container.NewVBox(
		widget.NewLabel("Archive folder"),
		container.NewBorder(nil, nil, nil, browse, p.outDir),
		widget.NewLabel("Quality"),
		p.quality,
		widget.NewLabel("File layout"),
		p.layout,
		hint("Timeline: YYYY/YYYY-MM-DD - SS.EE - Show - Title.mp3 · Seasons: Show/Season NN/SS.EE - Title.mp3"),
		widget.NewLabel("Shows"),
		container.NewHBox(p.showMU, p.showPlus, p.showIne),
		seasons,
		hint("Existing files are detected and never re-downloaded; changing the layout moves them into place."),
	))

	// background
	p.interval = widget.NewSelect(intervalOptions, nil)
	p.interval.SetSelected(intervalLabel(cfg.CheckIntervalHours))
	p.autoStart = widget.NewCheck("Start archiving as soon as the app launches", nil)
	p.autoStart.SetChecked(cfg.AutoStart)
	p.startMin = widget.NewCheck("Start hidden in the system tray", nil)
	p.startMin.SetChecked(cfg.StartMinimized)
	p.closeToTray = widget.NewCheck("Closing the window keeps the app running in the tray", nil)
	p.closeToTray.SetChecked(cfg.CloseToTray)
	p.notifications = widget.NewCheck("Desktop notifications", nil)
	p.notifications.SetChecked(cfg.Notifications)
	p.autostartLogin = widget.NewCheck("Launch at login (adds a KDE/XDG autostart entry)", nil)
	p.autostartLogin.SetChecked(autostartEnabled())
	background := card("BACKGROUND", container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Check for new episodes"), nil, p.interval),
		p.autoStart, p.startMin, p.closeToTray, p.notifications, p.autostartLogin,
	))

	// limits (mirrors the overview sliders, as precise values)
	p.concurrency = widget.NewEntry()
	p.gap = widget.NewEntry()
	p.bw = widget.NewEntry()
	p.epGap = widget.NewEntry()
	p.syncLimits(cfg)
	limits := card("RATE LIMITS (exact values)", container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Parallel downloads", p.concurrency),
			widget.NewFormItem("Request gap (ms)", p.gap),
			widget.NewFormItem("Bandwidth (KiB/s)", p.bw),
			widget.NewFormItem("Episode gap (s)", p.epGap),
		),
		hint("1–8 parallel downloads; bandwidth 0 = unlimited. The defaults (2 / 1500 / 0 / 5) are gentle enough for an all-night back-catalogue run."),
	))

	p.saved = widget.NewLabel("")
	save := widget.NewButtonWithIcon("Save settings", nil, p.save)
	save.Importance = widget.HighImportance
	clearCache := widget.NewButton("Clear feed cache", func() {
		if dir, err := config.CacheDir(); err == nil {
			_ = os.RemoveAll(dir)
			p.saved.SetText("Feed cache cleared.")
		}
	})
	footer := container.NewHBox(save, clearCache, p.saved)

	left := container.NewVBox(account, archive)
	right := container.NewVBox(background, limits)
	body := container.NewGridWithColumns(2, left, right)
	p.box = container.NewVScroll(container.NewVBox(heading("Settings"), body, footer))
	return p
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (p *settingsPage) syncLimits(cfg config.Config) {
	p.concurrency.SetText(strconv.Itoa(cfg.Concurrency))
	p.gap.SetText(strconv.Itoa(cfg.RequestGapMS))
	p.bw.SetText(strconv.Itoa(cfg.BandwidthKBs))
	p.epGap.SetText(strconv.Itoa(cfg.EpisodeGapSec))
}

// collect builds a Config from the form.
func (p *settingsPage) collect() (config.Config, error) {
	cfg := p.ui.eng.Config()
	cfg.Email = strings.TrimSpace(p.email.Text)
	cfg.Password = p.password.Text
	cfg.OutDir = strings.TrimSpace(p.outDir.Text)
	if cfg.OutDir == "" {
		return cfg, errors.New("choose an archive folder")
	}
	if strings.HasPrefix(cfg.OutDir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.OutDir = filepath.Join(home, strings.TrimPrefix(cfg.OutDir, "~"))
		}
	}
	cfg.Quality = "hq"
	if p.quality.Selected == p.quality.Options[1] {
		cfg.Quality = "sq"
	}
	cfg.Layout = "chronological"
	if p.layout.Selected == p.layout.Options[1] {
		cfg.Layout = "seasons"
	}
	var shows []string
	if p.showMU.Checked {
		shows = append(shows, "mu")
	}
	if p.showPlus.Checked {
		shows = append(shows, "muplus")
	}
	if p.showIne.Checked {
		shows = append(shows, "inescapable")
	}
	if len(shows) == 0 {
		return cfg, errors.New("select at least one show")
	}
	if len(shows) == 3 {
		shows = nil
	}
	cfg.Shows = shows
	var err error
	if cfg.MinSeason, err = atoiOpt(p.minSeason.Text); err != nil {
		return cfg, errors.New("season range must be numeric")
	}
	if cfg.MaxSeason, err = atoiOpt(p.maxSeason.Text); err != nil {
		return cfg, errors.New("season range must be numeric")
	}
	if cfg.Concurrency, err = atoiOpt(p.concurrency.Text); err != nil || cfg.Concurrency < 1 {
		return cfg, errors.New("parallel downloads must be 1–8")
	}
	if cfg.RequestGapMS, err = atoiOpt(p.gap.Text); err != nil {
		return cfg, errors.New("request gap must be a number of milliseconds")
	}
	if cfg.BandwidthKBs, err = atoiOpt(p.bw.Text); err != nil {
		return cfg, errors.New("bandwidth cap must be a number")
	}
	if cfg.EpisodeGapSec, err = atoiOpt(p.epGap.Text); err != nil {
		return cfg, errors.New("episode gap must be a number of seconds")
	}
	cfg.CheckIntervalHours = intervalHours(p.interval.Selected)
	cfg.AutoStart = p.autoStart.Checked
	cfg.StartMinimized = p.startMin.Checked
	cfg.CloseToTray = p.closeToTray.Checked
	cfg.Notifications = p.notifications.Checked
	cfg.Normalize()
	return cfg, nil
}

func atoiOpt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

// applyToEngine pushes the form to the engine without necessarily saving.
func (p *settingsPage) applyToEngine() error {
	cfg, err := p.collect()
	if err != nil {
		return err
	}
	if cfg.Email == "" || cfg.Password == "" {
		if len(cfg.Feeds) == 0 {
			return errors.New("enter your MU email and password in Settings first")
		}
	}
	p.ui.eng.SetConfig(cfg)
	p.ui.cfg = cfg
	p.ui.overview.loadLimits(cfg)
	return nil
}

func (p *settingsPage) save() {
	cfg, err := p.collect()
	if err != nil {
		p.saved.SetText("⚠ " + err.Error())
		return
	}
	if err := cfg.Save(); err != nil {
		p.saved.SetText("⚠ could not save: " + err.Error())
		return
	}
	p.ui.eng.SetConfig(cfg)
	p.ui.cfg = cfg
	p.ui.overview.loadLimits(cfg)
	if cfg.CheckIntervalHours > 0 {
		if !p.ui.eng.Watching() {
			p.ui.eng.StartWatch(time.Duration(cfg.CheckIntervalHours)*time.Hour, false)
		}
	} else {
		p.ui.eng.StopWatch()
	}
	if err := setAutostart(p.autostartLogin.Checked); err != nil {
		p.saved.SetText("Saved, but autostart entry failed: " + err.Error())
		return
	}
	p.saved.SetText("Saved ✓")
}

func (p *settingsPage) browse() {
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return
		}
		p.outDir.SetText(u.Path())
	}, p.ui.win)
	if cur := strings.TrimSpace(p.outDir.Text); cur != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(cur)); err == nil {
			d.SetLocation(l)
		}
	}
	// Show before Resize: in Fyne 2.8 Resize() consults MinSize(), which
	// dereferences the dialog window that only exists after Show().
	d.Show()
	d.Resize(fyne.NewSize(800, 600))
}

func (p *settingsPage) testLogin() {
	email := strings.TrimSpace(p.email.Text)
	pass := p.password.Text
	if email == "" || pass == "" {
		p.loginStatus.SetText("Enter your email and password first.")
		return
	}
	p.loginStatus.SetText("Signing in…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		plan, err := p.ui.eng.TestLogin(ctx, email, pass)
		fyne.Do(func() {
			if err != nil {
				p.loginStatus.SetText("✗ " + err.Error())
				return
			}
			if plan == "" {
				plan = "subscription details not shown on dashboard"
			}
			p.loginStatus.SetText(fmt.Sprintf("✓ Signed in (%s)", plan))
		})
	}()
}

// --- XDG autostart ---------------------------------------------------------

func autostartPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autostart", "mu-archiver.desktop")
}

// iconPath returns an absolute path to the application icon, writing the
// embedded image next to the config file so menu and autostart entries can
// reference it without depending on the icon theme cache.
func iconPath() string {
	dir, err := config.Dir()
	if err != nil {
		return "mu-archiver"
	}
	p := filepath.Join(dir, "mu-archiver.png")
	if b, err := os.ReadFile(p); err == nil && string(b) == string(assets.Icon.Content()) {
		return p
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "mu-archiver"
	}
	if err := os.WriteFile(p, assets.Icon.Content(), 0o644); err != nil {
		return "mu-archiver"
	}
	return p
}

func autostartEnabled() bool {
	_, err := os.Stat(autostartPath())
	return err == nil
}

func setAutostart(on bool) error {
	path := autostartPath()
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if p, err := exec.LookPath("mu-archiver"); err == nil && filepath.Base(exe) == "mu-archiver" {
		exe = p
	}
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=MU Archiver
Comment=Keep the Mysterious Universe archive up to date in the background
Exec=%s --minimized
Icon=%s
Terminal=false
X-GNOME-Autostart-enabled=true
X-KDE-autostart-after=panel
`, exe, iconPath())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(entry), 0o644)
}

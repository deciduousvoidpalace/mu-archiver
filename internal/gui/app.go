// Package gui is the desktop front-end (Fyne) for the archiver engine. It is
// designed to live in the system tray on a KDE Plasma desktop: closing the
// window hides it, the tray menu controls the run, desktop notifications
// report progress, and a background schedule keeps the archive current.
package gui

import (
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"mu-dl/internal/archiver"
	"mu-dl/internal/assets"
	"mu-dl/internal/config"
)

// AppID is the Fyne/D-Bus application identifier.
const AppID = "org.muarchiver.MUArchiver"

// Version is shown in the About area.
const Version = "2.0.0"

// App holds the UI state.
type App struct {
	fyne fyne.App
	win  fyne.Window
	eng  *archiver.Engine
	cfg  config.Config
	tray desktop.App

	mu              sync.Mutex
	logLines        []string
	logDirty        bool
	itemsDirty      bool
	lastState       archiver.State
	lastTrayLabel   string
	lastTrayAt      time.Time
	lastNotifiedRun time.Time

	// header
	statusPill *pill
	startBtn   *widget.Button
	pauseBtn   *widget.Button
	stopBtn    *widget.Button

	// pages
	pages   map[string]fyne.CanvasObject
	navBtns map[string]*widget.Button
	content *fyne.Container

	overview *overviewPage
	library  *libraryPage
	settings *settingsPage
	logPage  *logPage

	trayShow, trayToggle, trayPause, trayStatus *fyne.MenuItem
}

// Run starts the desktop application. minimized starts it hidden in the tray.
func Run(cfg config.Config, minimized bool) {
	a := app.NewWithID(AppID)
	a.SetIcon(assets.Icon)
	a.Settings().SetTheme(spaceTheme{})

	ui := &App{fyne: a, cfg: cfg, eng: archiver.New(cfg), pages: map[string]fyne.CanvasObject{}, navBtns: map[string]*widget.Button{}}
	ui.win = a.NewWindow("MU Archiver")
	ui.win.Resize(fyne.NewSize(1120, 740))
	ui.win.CenterOnScreen()

	ui.eng.Subscribe(ui.onEvent)
	ui.build()
	ui.setupTray()

	closeToTray := cfg.CloseToTray
	if ui.tray == nil {
		closeToTray = false
	}
	ui.win.SetCloseIntercept(func() {
		if closeToTray && ui.settings.closeToTray.Checked {
			ui.win.Hide()
			ui.notify("MU Archiver keeps running", "Still archiving in the background — find it in the system tray.", true)
			return
		}
		ui.quit()
	})

	// live refresh
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			fyne.Do(ui.refresh)
		}
	}()

	// Nothing touches the network until the user asks (or opted in): the
	// background schedule's first pass runs after one interval, unless
	// "start archiving on launch" is on.
	switch {
	case cfg.CheckIntervalHours > 0:
		ui.eng.StartWatch(time.Duration(cfg.CheckIntervalHours)*time.Hour, cfg.AutoStart && cfg.Email != "")
	case cfg.AutoStart && cfg.Email != "":
		_ = ui.eng.Start(archiver.RunOptions{})
	}

	if minimized && ui.tray != nil {
		// keep the window hidden; the app stays alive thanks to the tray
		ui.win.SetContent(ui.win.Content())
		a.Run()
		return
	}
	ui.win.ShowAndRun()
}

func (ui *App) quit() {
	ui.eng.StopWatch()
	if ui.eng.Running() {
		ui.eng.Stop()
		done := make(chan struct{})
		go func() { ui.eng.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	ui.fyne.Quit()
}

// --- layout --------------------------------------------------------------

func (ui *App) build() {
	ui.overview = newOverviewPage(ui)
	ui.library = newLibraryPage(ui)
	ui.settings = newSettingsPage(ui)
	ui.logPage = newLogPage(ui)

	ui.pages["overview"] = ui.overview.box
	ui.pages["library"] = ui.library.box
	ui.pages["settings"] = ui.settings.box
	ui.pages["log"] = ui.logPage.box

	ui.content = container.NewStack(ui.pages["overview"], ui.pages["library"], ui.pages["settings"], ui.pages["log"])
	nav := ui.buildNav()
	header := ui.buildHeader()
	ui.showPage("overview")

	root := container.NewBorder(header, nil, nav, nil, container.NewPadded(ui.content))
	bg := canvas.NewRectangle(colBackground)
	ui.win.SetContent(container.NewStack(bg, root))
}

func (ui *App) buildNav() fyne.CanvasObject {
	items := []struct {
		id, label string
		icon      fyne.Resource
	}{
		{"overview", "Overview", theme.HomeIcon()},
		{"library", "Library", theme.ListIcon()},
		{"settings", "Settings", theme.SettingsIcon()},
		{"log", "Activity", theme.DocumentIcon()},
	}
	var btns []fyne.CanvasObject
	for _, it := range items {
		id := it.id
		b := widget.NewButtonWithIcon(it.label, it.icon, func() { ui.showPage(id) })
		b.Alignment = widget.ButtonAlignLeading
		b.Importance = widget.LowImportance
		ui.navBtns[id] = b
		btns = append(btns, b)
	}
	logo := canvas.NewImageFromResource(assets.Icon)
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(72, 72))
	title := canvas.NewText("MU Archiver", colText)
	title.TextSize = 18
	title.TextStyle.Bold = true
	title.Alignment = fyne.TextAlignCenter
	sub := muted("v" + Version)
	sub.Alignment = fyne.TextAlignCenter

	folderBtn := widget.NewButtonWithIcon("Open archive folder", theme.FolderOpenIcon(), ui.openArchiveFolder)
	folderBtn.Importance = widget.LowImportance
	folderBtn.Alignment = widget.ButtonAlignLeading

	col := container.NewVBox(
		container.NewPadded(container.NewVBox(logo, title, sub)),
		widget.NewSeparator(),
	)
	col.Add(container.NewVBox(btns...))
	side := container.NewBorder(col, container.NewVBox(widget.NewSeparator(), folderBtn), nil, nil)
	bg := canvas.NewRectangle(colSurface)
	wrap := container.NewStack(bg, container.NewPadded(side))
	return container.New(&fixedWidth{w: 200}, wrap)
}

// fixedWidth lays out a single child at a fixed width.
type fixedWidth struct{ w float32 }

func (f *fixedWidth) MinSize(objs []fyne.CanvasObject) fyne.Size {
	h := float32(0)
	if len(objs) > 0 {
		h = objs[0].MinSize().Height
	}
	return fyne.NewSize(f.w, h)
}

func (f *fixedWidth) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(fyne.NewSize(f.w, size.Height))
	}
}

func (ui *App) buildHeader() fyne.CanvasObject {
	ui.statusPill = newPill("Idle", colMuted)
	ui.startBtn = widget.NewButtonWithIcon("Archive now", theme.DownloadIcon(), ui.startRun)
	ui.startBtn.Importance = widget.HighImportance
	ui.pauseBtn = widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), ui.togglePause)
	ui.stopBtn = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), ui.eng.Stop)
	ui.stopBtn.Importance = widget.DangerImportance
	scanBtn := widget.NewButtonWithIcon("Check for new", theme.ViewRefreshIcon(), ui.scanOnly)
	scanBtn.Importance = widget.MediumImportance

	right := container.NewHBox(ui.statusPill.box, scanBtn, ui.startBtn, ui.pauseBtn, ui.stopBtn)
	bg := canvas.NewRectangle(colSurface)
	line := canvas.NewRectangle(colBorder)
	line.SetMinSize(fyne.NewSize(0, 1))
	h := container.NewBorder(nil, line, nil, container.NewPadded(right), nil)
	return container.NewStack(bg, h)
}

func (ui *App) showPage(id string) {
	for k, p := range ui.pages {
		if k == id {
			p.Show()
		} else {
			p.Hide()
		}
	}
	for k, b := range ui.navBtns {
		if k == id {
			b.Importance = widget.HighImportance
		} else {
			b.Importance = widget.LowImportance
		}
		b.Refresh()
	}
	if id == "library" {
		ui.library.reload()
	}
	if id == "log" {
		ui.logPage.reload()
	}
	ui.content.Refresh()
}

// --- actions -------------------------------------------------------------

func (ui *App) startRun() {
	if err := ui.settings.applyToEngine(); err != nil {
		ui.showError(err)
		return
	}
	if ui.eng.Running() {
		return
	}
	if err := ui.eng.Start(archiver.RunOptions{}); err != nil {
		ui.showError(err)
	}
}

func (ui *App) scanOnly() {
	if err := ui.settings.applyToEngine(); err != nil {
		ui.showError(err)
		return
	}
	if ui.eng.Running() {
		return
	}
	if err := ui.eng.Start(archiver.RunOptions{ScanOnly: true}); err != nil {
		ui.showError(err)
	}
}

func (ui *App) togglePause() {
	if ui.eng.Paused() {
		ui.eng.Resume()
	} else {
		ui.eng.Pause()
	}
}

func (ui *App) openArchiveFolder() {
	dir := ui.eng.Config().OutDir
	_ = os.MkdirAll(dir, 0o755)
	abs, _ := filepath.Abs(dir)
	_ = ui.fyne.OpenURL(&url.URL{Scheme: "file", Path: abs})
}

func (ui *App) showError(err error) {
	fyne.Do(func() {
		ui.win.Show()
		widget.ShowPopUp(container.NewPadded(widget.NewLabel("Error: "+err.Error())), ui.win.Canvas())
	})
}

// --- events / refresh ----------------------------------------------------

func (ui *App) onEvent(ev archiver.Event) {
	ui.mu.Lock()
	if ev.Kind != archiver.KindState && ev.Message != "" {
		ui.logLines = append(ui.logLines, ev.Time.Format("15:04:05")+"  "+ev.Message)
		if len(ui.logLines) > 3000 {
			ui.logLines = ui.logLines[len(ui.logLines)-3000:]
		}
		ui.logDirty = true
	}
	if ev.Kind == archiver.KindScanDone || ev.Kind == archiver.KindEpisode || ev.Kind == archiver.KindRunDone {
		ui.itemsDirty = true
	}
	ui.mu.Unlock()

	switch ev.Kind {
	case archiver.KindRunDone:
		if ev.Summary != nil && !ev.Summary.ScanOnly && (ev.Summary.Downloaded > 0 || ev.Summary.Failed > 0) {
			title := "Archive up to date"
			if ev.Summary.Failed > 0 {
				title = fmt.Sprintf("Archive finished with %s", plural(ev.Summary.Failed, "failure", "failures"))
			}
			ui.notify(title, ev.Message, false)
		}
		if ev.Summary != nil && ev.Summary.ScanOnly && ev.Summary.NewFound > 0 {
			ui.notify("New episodes available", fmt.Sprintf("%s not yet archived.", plural(ev.Summary.NewFound, "episode", "episodes")), false)
		}
	case archiver.KindRunFail:
		ui.notify("MU Archiver: run failed", ev.Message, false)
	case archiver.KindLoginFail:
		ui.notify("MU Archiver: login failed", ev.Message, false)
	case archiver.KindCooldown:
		ui.notify("MU Archiver is backing off", ev.Message, false)
	}
}

func (ui *App) notify(title, body string, always bool) {
	if !always && !ui.eng.Config().Notifications {
		return
	}
	ui.fyne.SendNotification(fyne.NewNotification(title, body))
}

func (ui *App) refresh() {
	st := ui.eng.Status()
	running := ui.eng.Running()

	// header
	label, col := stateLabel(st)
	ui.statusPill.set(label, col)
	if running {
		ui.startBtn.Disable()
		ui.stopBtn.Enable()
		ui.pauseBtn.Enable()
		if ui.eng.Paused() {
			ui.pauseBtn.SetText("Resume")
			ui.pauseBtn.SetIcon(theme.MediaPlayIcon())
		} else {
			ui.pauseBtn.SetText("Pause")
			ui.pauseBtn.SetIcon(theme.MediaPauseIcon())
		}
	} else {
		ui.startBtn.Enable()
		ui.stopBtn.Disable()
		ui.pauseBtn.Disable()
		ui.pauseBtn.SetText("Pause")
	}

	ui.overview.refresh(st, running)

	ui.mu.Lock()
	logDirty, itemsDirty := ui.logDirty, ui.itemsDirty
	ui.logDirty, ui.itemsDirty = false, false
	stateChanged := ui.lastState != st.State
	ui.lastState = st.State
	ui.mu.Unlock()
	if logDirty && ui.pages["log"].Visible() {
		ui.logPage.reload()
	}
	if itemsDirty && ui.pages["library"].Visible() {
		ui.library.reload()
	}
	if stateChanged || running {
		ui.refreshTray(st, running, stateChanged)
	}
}

func stateLabel(st archiver.Status) (string, color.Color) {
	switch st.State {
	case archiver.StateLoggingIn:
		return "Logging in", colPrimary
	case archiver.StateScanning:
		return "Scanning feeds", colPrimary
	case archiver.StateDownloading:
		return "Archiving", colAccent
	case archiver.StatePaused:
		return "Paused", colWarn
	case archiver.StateCoolingDown:
		return "Cooling down", colWarn
	case archiver.StateStopping:
		return "Stopping", colWarn
	case archiver.StateDone:
		return "Up to date", colAccent
	case archiver.StateFailed:
		return "Attention needed", colError
	}
	return "Idle", colMuted
}

// --- tray ----------------------------------------------------------------

func (ui *App) setupTray() {
	desk, ok := ui.fyne.(desktop.App)
	if !ok {
		return
	}
	ui.tray = desk
	ui.trayShow = fyne.NewMenuItem("Show MU Archiver", func() { fyne.Do(func() { ui.win.Show(); ui.win.RequestFocus() }) })
	ui.trayToggle = fyne.NewMenuItem("Archive now", func() { fyne.Do(ui.startRun) })
	ui.trayPause = fyne.NewMenuItem("Pause", func() { fyne.Do(ui.togglePause) })
	ui.trayStatus = fyne.NewMenuItem("Idle", nil)
	ui.trayStatus.Disabled = true
	desk.SetSystemTrayMenu(ui.trayMenu())
	desk.SetSystemTrayIcon(assets.Tray)
	desk.SetSystemTrayWindow(ui.win)
}

func (ui *App) trayMenu() *fyne.Menu {
	check := fyne.NewMenuItem("Check for new episodes", func() { fyne.Do(ui.scanOnly) })
	stop := fyne.NewMenuItem("Stop", func() { ui.eng.Stop() })
	quit := fyne.NewMenuItem("Quit", func() { fyne.Do(ui.quit) })
	quit.IsQuit = true
	return fyne.NewMenu("MU Archiver",
		ui.trayStatus,
		fyne.NewMenuItemSeparator(),
		ui.trayShow,
		fyne.NewMenuItemSeparator(),
		ui.trayToggle,
		ui.trayPause,
		stop,
		check,
		fyne.NewMenuItemSeparator(),
		quit,
	)
}

// refreshTray rebuilds the tray menu, but no more than every few seconds
// unless the state changed: each rebuild is a round-trip to the desktop's
// StatusNotifier host.
func (ui *App) refreshTray(st archiver.Status, running bool, force bool) {
	if ui.tray == nil {
		return
	}
	label, _ := stateLabel(st)
	status := label
	if running && st.Total > 0 {
		status = fmt.Sprintf("%s · %d/%d archived", label, st.Archived, st.Total)
		if st.Speed > 0 {
			status += " · " + humanRate(st.Speed)
		}
	} else if !running && st.Total > 0 {
		status = fmt.Sprintf("%s · %d of %d archived", label, st.Archived, st.Total)
	}
	if !force && status == ui.lastTrayLabel {
		return
	}
	if !force && time.Since(ui.lastTrayAt) < 3*time.Second {
		return
	}
	ui.lastTrayLabel, ui.lastTrayAt = status, time.Now()
	ui.trayStatus.Label = status
	ui.trayToggle.Disabled = running
	ui.trayPause.Disabled = !running
	if ui.eng.Paused() {
		ui.trayPause.Label = "Resume"
	} else {
		ui.trayPause.Label = "Pause"
	}
	ui.tray.SetSystemTrayMenu(ui.trayMenu())
}

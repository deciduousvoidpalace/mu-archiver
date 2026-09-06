package gui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"mu-dl/internal/archiver"
	"mu-dl/internal/config"
	"mu-dl/internal/downloader"
	"mu-dl/internal/ratelimit"
	"mu-dl/internal/util"
)

type overviewPage struct {
	ui  *App
	box fyne.CanvasObject

	stTotal, stArchived, stRemaining, stDisk *stat
	progress                                 *widget.ProgressBar
	progressText                             *widget.Label
	stateText                                *widget.Label

	transfers    []downloader.Progress
	transferList *widget.List

	accountText  *widget.Label
	scheduleText *widget.Label

	gapRow, bwRow, epRow, concRow *sliderRow
	applying                      bool
}

func newOverviewPage(ui *App) *overviewPage {
	p := &overviewPage{ui: ui}

	p.stTotal = newStat("episodes in catalogue", colText)
	p.stArchived = newStat("archived", colAccent)
	p.stRemaining = newStat("remaining", colPrimary)
	p.stDisk = newStat("on disk", colMoon)
	stats := container.NewGridWithColumns(4, p.stTotal.box, p.stArchived.box, p.stRemaining.box, p.stDisk.box)

	p.progress = widget.NewProgressBar()
	p.progress.TextFormatter = func() string { return "" }
	p.progressText = widget.NewLabel("Nothing scanned yet — press “Archive now” or “Check for new”.")
	p.progressText.Wrapping = fyne.TextWrapWord
	p.stateText = widget.NewLabel("")
	p.stateText.TextStyle.Italic = true
	p.stateText.Wrapping = fyne.TextWrapWord
	progressCard := card("PROGRESS", container.NewVBox(p.progress, p.progressText, p.stateText))

	p.transferList = widget.NewList(
		func() int { return len(p.transfers) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			bar := widget.NewProgressBar()
			bar.TextFormatter = func() string { return "" }
			info := widget.NewLabel("")
			info.Alignment = fyne.TextAlignTrailing
			return container.NewVBox(container.NewBorder(nil, nil, nil, info, name), bar)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.transfers) {
				return
			}
			t := p.transfers[i]
			row := o.(*fyne.Container)
			top := row.Objects[0].(*fyne.Container)
			bar := row.Objects[1].(*widget.ProgressBar)
			var name, info *widget.Label
			for _, obj := range top.Objects {
				if l, ok := obj.(*widget.Label); ok {
					if l.Alignment == fyne.TextAlignTrailing {
						info = l
					} else {
						name = l
					}
				}
			}
			name.SetText(t.Name)
			if t.Total > 0 {
				bar.SetValue(float64(t.Done) / float64(t.Total))
				info.SetText(fmt.Sprintf("%s / %s · %s", util.HumanBytes(t.Done), util.HumanBytes(t.Total), humanRate(t.Speed)))
			} else {
				bar.SetValue(0)
				info.SetText(fmt.Sprintf("%s · %s", util.HumanBytes(t.Done), humanRate(t.Speed)))
			}
			if t.Attempt > 1 {
				info.SetText(info.Text + fmt.Sprintf(" · retry %d", t.Attempt))
			}
		},
	)
	transfersCard := card("ACTIVE DOWNLOADS", container.NewGridWrap(fyne.NewSize(10, 10)))
	transfersCard = card("ACTIVE DOWNLOADS", container.New(&minHeight{h: 170}, p.transferList))

	// politeness controls (live)
	p.gapRow = newSliderRow("Gap between requests", 0, 10000, 250, func(v float64) string {
		return fmt.Sprintf("%.2fs", v/1000)
	}, func(float64) { p.applyLimits() })
	p.bwRow = newSliderRow("Bandwidth cap", 0, 20480, 256, func(v float64) string {
		if v <= 0 {
			return "unlimited"
		}
		if v >= 1024 {
			return fmt.Sprintf("%.1f MiB/s", v/1024)
		}
		return fmt.Sprintf("%d KiB/s", int(v))
	}, func(float64) { p.applyLimits() })
	p.epRow = newSliderRow("Pause between episodes", 0, 120, 1, func(v float64) string {
		return fmt.Sprintf("%ds", int(v))
	}, func(float64) { p.applyLimits() })
	p.concRow = newSliderRow("Parallel downloads (next run)", 1, 6, 1, func(v float64) string {
		return fmt.Sprintf("%d", int(v))
	}, func(float64) { p.applyLimits() })
	gentle := widget.NewButton("Gentle", func() { p.preset(3000, 0, 15, 1) })
	balanced := widget.NewButton("Balanced", func() { p.preset(1500, 0, 5, 2) })
	brisk := widget.NewButton("Brisk", func() { p.preset(500, 0, 1, 4) })
	presets := container.NewHBox(widget.NewLabel("Presets:"), gentle, balanced, brisk)
	note := hint("Changes apply immediately, even mid-download. The archiver also backs off automatically if the server answers 429/503.")
	politeCard := card("SELF RATE-LIMITING", container.NewVBox(p.gapRow.box, p.bwRow.box, p.epRow.box, p.concRow.box, presets, note))

	p.accountText = widget.NewLabel("Not signed in")
	p.accountText.Wrapping = fyne.TextWrapWord
	p.scheduleText = widget.NewLabel("")
	p.scheduleText.Wrapping = fyne.TextWrapWord
	accountCard := card("ACCOUNT & SCHEDULE", container.NewVBox(p.accountText, p.scheduleText))

	left := container.NewVBox(progressCard, transfersCard)
	right := container.NewVBox(accountCard, politeCard)
	body := container.NewGridWithColumns(2, left, right)
	p.box = container.NewVScroll(container.NewVBox(heading("Overview"), stats, body))
	p.loadLimits(ui.cfg)
	return p
}

// minHeight gives a scrollable child a guaranteed height inside a VBox.
type minHeight struct{ h float32 }

func (m *minHeight) MinSize(objs []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	if len(objs) > 0 {
		w = objs[0].MinSize().Width
	}
	return fyne.NewSize(w, m.h)
}

func (m *minHeight) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (p *overviewPage) loadLimits(cfg config.Config) {
	p.applying = true
	p.gapRow.set(float64(cfg.RequestGapMS))
	p.bwRow.set(float64(cfg.BandwidthKBs))
	p.epRow.set(float64(cfg.EpisodeGapSec))
	p.concRow.set(float64(cfg.Concurrency))
	p.applying = false
}

func (p *overviewPage) preset(gapMS, bwKB, epSec, conc float64) {
	p.applying = true
	p.gapRow.set(gapMS)
	p.bwRow.set(bwKB)
	p.epRow.set(epSec)
	p.concRow.set(conc)
	p.applying = false
	p.applyLimits()
}

// applyLimits pushes the slider values to the engine and persists them.
func (p *overviewPage) applyLimits() {
	if p.applying {
		return
	}
	cfg := p.ui.eng.Config()
	cfg.RequestGapMS = int(p.gapRow.slider.Value)
	cfg.BandwidthKBs = int(p.bwRow.slider.Value)
	cfg.EpisodeGapSec = int(p.epRow.slider.Value)
	cfg.Concurrency = int(p.concRow.slider.Value)
	p.ui.eng.Limiter().Set(ratelimit.Limits{RequestGap: cfg.RequestGap(), Bandwidth: cfg.BandwidthBytes(), EpisodeGap: cfg.EpisodeGap()})
	p.ui.eng.SetConfig(cfg)
	p.ui.cfg = cfg
	_ = cfg.Save()
	if p.ui.settings != nil {
		p.ui.settings.syncLimits(cfg)
	}
}

func (p *overviewPage) refresh(st archiver.Status, running bool) {
	p.stTotal.set(fmtInt(st.Total))
	p.stArchived.set(fmtInt(st.Archived))
	p.stRemaining.set(fmtInt(st.Total - st.Archived))
	p.stDisk.set(util.HumanBytes(st.BytesTotal))

	if st.Total > 0 {
		p.progress.SetValue(float64(st.Archived) / float64(st.Total))
	} else {
		p.progress.SetValue(0)
	}
	switch {
	case running && st.State == archiver.StateScanning:
		p.progressText.SetText(fmt.Sprintf("Reading feeds… %d of %d", st.SourcesDone, st.Sources))
	case running && st.Total > 0:
		var remaining int64
		for _, t := range st.Transfers {
			remaining += t.Total - t.Done
		}
		txt := fmt.Sprintf("%d of %d archived · %d fetched this session (%s)", st.Archived, st.Total, st.Downloaded, util.HumanBytes(st.BytesRun))
		if st.Speed > 0 {
			txt += " · " + humanRate(st.Speed)
		}
		if st.Failed > 0 {
			txt += fmt.Sprintf(" · %d failed", st.Failed)
		}
		p.progressText.SetText(txt)
	case st.Total > 0:
		p.progressText.SetText(fmt.Sprintf("%d of %d episodes archived · %d to go", st.Archived, st.Total, st.Total-st.Archived))
	}
	msg := st.Message
	if !st.CooldownUntil.IsZero() {
		msg = fmt.Sprintf("Server asked us to slow down — resuming in %s", time.Until(st.CooldownUntil).Round(time.Second))
	}
	if !running && st.State == archiver.StateFailed {
		msg = "Last run failed: " + st.Message
	}
	p.stateText.SetText(msg)

	p.transfers = st.Transfers
	p.transferList.Refresh()

	acct := "Not signed in"
	if st.Email != "" {
		acct = st.Email
		if st.Plan != "" {
			acct += " · " + st.Plan
		}
		if st.LoggedIn {
			acct += " · session active"
		}
	}
	p.accountText.SetText(acct)
	sched := "Background checks off — enable them in Settings to catch new episodes automatically."
	if !st.NextScan.IsZero() {
		sched = fmt.Sprintf("Next automatic check: %s", st.NextScan.Format("Mon 15:04"))
	}
	if !st.LastScan.IsZero() {
		sched += fmt.Sprintf("\nLast scan: %s", st.LastScan.Format("Mon 15:04"))
	}
	p.scheduleText.SetText(sched)
}

func fmtInt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

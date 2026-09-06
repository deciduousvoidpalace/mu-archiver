package gui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type logPage struct {
	ui    *App
	box   fyne.CanvasObject
	lines []string
	list  *widget.List
}

func newLogPage(ui *App) *logPage {
	p := &logPage{ui: ui}
	p.list = widget.NewList(
		func() int { return len(p.lines) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle.Monospace = true
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(p.lines) {
				o.(*widget.Label).SetText(p.lines[i])
			}
		},
	)
	copyBtn := widget.NewButton("Copy to clipboard", func() {
		ui.fyne.Clipboard().SetContent(strings.Join(p.lines, "\n"))
	})
	clearBtn := widget.NewButton("Clear", func() {
		ui.mu.Lock()
		ui.logLines = nil
		ui.mu.Unlock()
		p.reload()
	})
	top := container.NewBorder(nil, nil, heading("Activity"), container.NewHBox(copyBtn, clearBtn))
	p.box = container.NewBorder(top, nil, nil, nil, p.list)
	return p
}

func (p *logPage) reload() {
	p.ui.mu.Lock()
	p.lines = append(p.lines[:0], p.ui.logLines...)
	p.ui.mu.Unlock()
	p.list.Refresh()
	if len(p.lines) > 0 {
		p.list.ScrollToBottom()
	}
}

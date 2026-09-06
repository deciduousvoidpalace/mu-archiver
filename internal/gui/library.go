package gui

import (
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"mu-dl/internal/archiver"
	"mu-dl/internal/feed"
	"mu-dl/internal/util"
)

type libraryPage struct {
	ui  *App
	box fyne.CanvasObject

	all       []archiver.Item
	rows      []archiver.Item
	table     *widget.Table
	summary   *widget.Label
	search    *widget.Entry
	showSel   *widget.Select
	statusSel *widget.Select
	detail    *widget.Label
}

var libColumns = []struct {
	name  string
	width float32
}{{"", 30}, {"Show", 160}, {"No.", 60}, {"Title", 330}, {"Size", 84}, {"Published", 96}}

func newLibraryPage(ui *App) *libraryPage {
	p := &libraryPage{ui: ui}
	p.search = widget.NewEntry()
	p.search.SetPlaceHolder("Search titles…")
	p.search.OnChanged = func(string) { p.filter() }
	p.showSel = widget.NewSelect([]string{"All shows", feed.ShowMU, feed.ShowPlus, feed.ShowInescapable}, nil)
	p.showSel.SetSelected("All shows")
	p.statusSel = widget.NewSelect([]string{"Any status", "Archived", "Pending", "Failed"}, nil)
	p.statusSel.SetSelected("Any status")
	filters := container.NewBorder(nil, nil, container.NewHBox(p.showSel, p.statusSel), nil, p.search)

	p.table = widget.NewTable(
		func() (int, int) { return len(p.rows), len(libColumns) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			l := o.(*widget.Label)
			if id.Row >= len(p.rows) {
				l.SetText("")
				return
			}
			it := p.rows[id.Row]
			l.TextStyle = fyne.TextStyle{}
			l.Alignment = fyne.TextAlignLeading
			switch id.Col {
			case 0:
				l.Alignment = fyne.TextAlignCenter
				l.SetText(statusGlyph(it.Status))
			case 1:
				l.SetText(it.Show)
			case 2:
				l.SetText(it.Number())
			case 3:
				t := it.Title
				if it.Show == feed.ShowInescapable && it.Plus {
					t += "  [Plus+]"
				}
				l.SetText(t)
			case 4:
				l.Alignment = fyne.TextAlignTrailing
				if it.Size > 0 {
					l.SetText(util.HumanBytes(it.Size))
				} else {
					l.SetText("")
				}
			case 5:
				if it.Published.IsZero() {
					l.SetText("")
				} else {
					l.SetText(it.Published.Format("2006-01-02"))
				}
			}
		},
	)
	p.table.ShowHeaderRow = true
	p.table.CreateHeader = func() fyne.CanvasObject {
		l := widget.NewLabel("")
		l.TextStyle.Bold = true
		return l
	}
	p.table.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		o.(*widget.Label).SetText(libColumns[id.Col].name)
	}
	for i, c := range libColumns {
		p.table.SetColumnWidth(i, c.width)
	}
	p.detail = widget.NewLabel("")
	p.detail.Wrapping = fyne.TextWrapWord
	p.table.OnSelected = func(id widget.TableCellID) {
		if id.Row < len(p.rows) {
			it := p.rows[id.Row]
			d := fmt.Sprintf("%s  ·  %s", it.RawTitle, it.Path)
			if it.Error != "" {
				d += "\nLast error: " + it.Error
			}
			if it.Duration > 0 {
				d += fmt.Sprintf("  ·  %s", it.Duration.Round(60e9))
			}
			p.detail.SetText(d)
		}
	}
	p.summary = widget.NewLabel("Run a scan to populate the catalogue.")
	legend := hint("✓ archived   ● downloading   ◐ partial   ✗ failed   · pending")
	bottom := container.NewVBox(p.detail, p.summary, legend)
	p.box = container.NewBorder(container.NewVBox(heading("Library"), filters), bottom, nil, nil, p.table)
	// wire the filters only once every widget they touch exists
	p.showSel.OnChanged = func(string) { p.filter() }
	p.statusSel.OnChanged = func(string) { p.filter() }
	return p
}

func statusGlyph(s archiver.ItemStatus) string {
	switch s {
	case archiver.Archived:
		return "✓"
	case archiver.Active:
		return "●"
	case archiver.Partial:
		return "◐"
	case archiver.Failed:
		return "✗"
	}
	return "·"
}

func (p *libraryPage) reload() {
	p.all = p.ui.eng.Items()
	p.filter()
}

func (p *libraryPage) filter() {
	q := strings.ToLower(strings.TrimSpace(p.search.Text))
	show := p.showSel.Selected
	status := p.statusSel.Selected
	p.rows = p.rows[:0]
	var archived int
	var bytes int64
	for _, it := range p.all {
		if it.Status == archiver.Archived {
			archived++
			bytes += it.Size
		}
		if show != "" && show != "All shows" && it.Show != show {
			continue
		}
		switch status {
		case "Archived":
			if it.Status != archiver.Archived {
				continue
			}
		case "Pending":
			if it.Status == archiver.Archived || it.Status == archiver.Failed {
				continue
			}
		case "Failed":
			if it.Status != archiver.Failed {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Title), q) && !strings.Contains(strings.ToLower(it.RawTitle), q) && !strings.Contains(it.Number(), q) {
			continue
		}
		p.rows = append(p.rows, it)
	}
	// one timeline, newest first: MU and Plus+ interleave by release date
	sort.SliceStable(p.rows, func(i, j int) bool {
		a, b := p.rows[i], p.rows[j]
		if !a.Published.Equal(b.Published) {
			return a.Published.After(b.Published)
		}
		if a.Show != b.Show {
			return showRank(a.Show) < showRank(b.Show)
		}
		if a.Season != b.Season {
			return a.Season > b.Season
		}
		return a.Episode.Episode > b.Episode.Episode
	})
	if len(p.all) == 0 {
		p.summary.SetText("Run a scan to populate the catalogue.")
	} else {
		p.summary.SetText(fmt.Sprintf("Showing %d of %d episodes · %d archived (%s)", len(p.rows), len(p.all), archived, util.HumanBytes(bytes)))
	}
	p.table.Refresh()
}

func showRank(show string) int {
	switch show {
	case feed.ShowMU:
		return 0
	case feed.ShowPlus:
		return 1
	case feed.ShowInescapable:
		return 2
	}
	return 3
}

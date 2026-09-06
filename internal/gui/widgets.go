package gui

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"mu-dl/internal/util"
)

// card wraps content in a rounded surface with an optional small uppercase
// title.
func card(title string, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colSurface)
	bg.CornerRadius = 14
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1
	var inner fyne.CanvasObject = content
	if title != "" {
		t := canvas.NewText(title, colMuted)
		t.TextSize = 11
		t.TextStyle.Bold = true
		inner = container.NewVBox(t, content)
	}
	return container.NewStack(bg, container.NewPadded(container.NewPadded(inner)))
}

// stat is a big-number tile.
type stat struct {
	box   fyne.CanvasObject
	value *canvas.Text
	label *canvas.Text
}

func newStat(label string, accent color.Color) *stat {
	v := canvas.NewText("—", accent)
	v.TextSize = 30
	v.TextStyle.Bold = true
	l := canvas.NewText(label, colMuted)
	l.TextSize = 12
	s := &stat{value: v, label: l}
	s.box = card("", container.NewVBox(v, l))
	return s
}

func (s *stat) set(v string) {
	if s.value.Text != v {
		s.value.Text = v
		s.value.Refresh()
	}
}

// pill is a rounded status badge.
type pill struct {
	box  fyne.CanvasObject
	bg   *canvas.Rectangle
	text *canvas.Text
}

func newPill(text string, c color.Color) *pill {
	bg := canvas.NewRectangle(withAlpha(c, 0x33))
	bg.CornerRadius = 999
	bg.StrokeColor = withAlpha(c, 0x99)
	bg.StrokeWidth = 1
	t := canvas.NewText(text, c)
	t.TextSize = 12
	t.TextStyle.Bold = true
	t.Alignment = fyne.TextAlignCenter
	p := &pill{bg: bg, text: t}
	pad := container.New(&padLayout{h: 12, v: 5}, t)
	p.box = container.NewStack(bg, pad)
	return p
}

func (p *pill) set(text string, c color.Color) {
	if p.text.Text == text && p.text.Color == c {
		return
	}
	p.text.Text = text
	p.text.Color = c
	p.bg.FillColor = withAlpha(c, 0x33)
	p.bg.StrokeColor = withAlpha(c, 0x99)
	p.text.Refresh()
	p.bg.Refresh()
}

func withAlpha(c color.Color, a uint8) color.NRGBA {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), a}
}

// padLayout pads a single object by fixed horizontal/vertical amounts.
type padLayout struct{ h, v float32 }

func (p *padLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.NewSize(0, 0)
	}
	m := objs[0].MinSize()
	return fyne.NewSize(m.Width+2*p.h, m.Height+2*p.v)
}

func (p *padLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) == 0 {
		return
	}
	objs[0].Move(fyne.NewPos(p.h, p.v))
	objs[0].Resize(fyne.NewSize(size.Width-2*p.h, size.Height-2*p.v))
}

// heading renders a section title.
func heading(text string) *canvas.Text {
	t := canvas.NewText(text, colText)
	t.TextSize = 20
	t.TextStyle.Bold = true
	return t
}

func muted(text string) *canvas.Text {
	t := canvas.NewText(text, colMuted)
	t.TextSize = 12
	return t
}

// hint is a wrapping, de-emphasised paragraph for explanatory text.
func hint(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.LowImportance
	return l
}

// sliderRow is a labelled slider with a live value read-out.
type sliderRow struct {
	box    fyne.CanvasObject
	slider *widget.Slider
	value  *widget.Label
	format func(float64) string
}

func newSliderRow(label string, min, max, step float64, format func(float64) string, changed func(float64)) *sliderRow {
	s := widget.NewSlider(min, max)
	s.Step = step
	v := widget.NewLabel("")
	v.Alignment = fyne.TextAlignTrailing
	r := &sliderRow{slider: s, value: v, format: format}
	s.OnChanged = func(f float64) {
		v.SetText(format(f))
		if changed != nil {
			changed(f)
		}
	}
	l := widget.NewLabel(label)
	r.box = container.NewBorder(nil, nil, l, v, s)
	return r
}

func (r *sliderRow) set(f float64) {
	r.slider.SetValue(f)
	r.value.SetText(r.format(f))
}

func humanRate(bps float64) string {
	if bps <= 0 {
		return "0 B/s"
	}
	return util.HumanBytes(int64(bps)) + "/s"
}

func eta(remaining int64, bps float64) string {
	if bps <= 0 || remaining <= 0 {
		return ""
	}
	d := time.Duration(float64(remaining)/bps) * time.Second
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("~%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("~%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("~%d days", int(d.Hours()/24))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

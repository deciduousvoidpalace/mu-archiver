package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Palette: deep-space indigo surfaces, violet primary, teal accent.
var (
	colBackground = color.NRGBA{0x0f, 0x0d, 0x1f, 0xff}
	colSurface    = color.NRGBA{0x18, 0x15, 0x2e, 0xff}
	colSurfaceHi  = color.NRGBA{0x21, 0x1d, 0x3d, 0xff}
	colBorder     = color.NRGBA{0x2d, 0x29, 0x50, 0xff}
	colText       = color.NRGBA{0xec, 0xe9, 0xfb, 0xff}
	colMuted      = color.NRGBA{0x9c, 0x97, 0xc2, 0xff}
	colPrimary    = color.NRGBA{0x8b, 0x5c, 0xf6, 0xff}
	colAccent     = color.NRGBA{0x2d, 0xd4, 0xbf, 0xff}
	colWarn       = color.NRGBA{0xfb, 0xbf, 0x24, 0xff}
	colError      = color.NRGBA{0xf8, 0x71, 0x71, 0xff}
	colMoon       = color.NRGBA{0xfd, 0xe6, 0x8a, 0xff}
)

type spaceTheme struct{}

var _ fyne.Theme = spaceTheme{}

func (spaceTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return colPrimary
	case theme.ColorNameButton:
		return colSurfaceHi
	case theme.ColorNameInputBackground:
		return color.NRGBA{0x14, 0x11, 0x28, 0xff}
	case theme.ColorNameInputBorder:
		return colBorder
	case theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return colSurfaceHi
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{0x6f, 0x6a, 0x96, 0xff}
	case theme.ColorNameDisabled:
		return color.NRGBA{0x5b, 0x56, 0x80, 0xff}
	case theme.ColorNameDisabledButton:
		return color.NRGBA{0x1c, 0x19, 0x33, 0xff}
	case theme.ColorNameHover:
		return color.NRGBA{0x8b, 0x5c, 0xf6, 0x2e}
	case theme.ColorNamePressed:
		return color.NRGBA{0x8b, 0x5c, 0xf6, 0x55}
	case theme.ColorNameFocus:
		return color.NRGBA{0x8b, 0x5c, 0xf6, 0x66}
	case theme.ColorNameSelection:
		return color.NRGBA{0x8b, 0x5c, 0xf6, 0x59}
	case theme.ColorNameSeparator:
		return colBorder
	case theme.ColorNameScrollBar:
		return color.NRGBA{0x4b, 0x46, 0x80, 0xff}
	case theme.ColorNameShadow:
		return color.NRGBA{0, 0, 0, 0x80}
	case theme.ColorNameSuccess:
		return colAccent
	case theme.ColorNameError:
		return colError
	case theme.ColorNameWarning:
		return colWarn
	case theme.ColorNameHeaderBackground:
		return colSurface
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (spaceTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (spaceTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (spaceTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNameScrollBar:
		return 10
	case theme.SizeNameScrollBarSmall:
		return 4
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return theme.DefaultTheme().Size(n)
}

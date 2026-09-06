// Package assets embeds the application icons.
package assets

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:generate go run ./gen

//go:embed icon.png
var iconPNG []byte

//go:embed tray.png
var trayPNG []byte

// Icon is the 512px application icon.
var Icon = fyne.NewStaticResource("mu-archiver.png", iconPNG)

// Tray is the 64px tray icon.
var Tray = fyne.NewStaticResource("mu-archiver-tray.png", trayPNG)

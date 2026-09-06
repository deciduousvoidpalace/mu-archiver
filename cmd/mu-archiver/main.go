// Command mu-archiver is the desktop application for archiving Mysterious
// Universe podcasts. It targets Linux desktops (built and tested for KDE
// Plasma on x86-64): it lives in the system tray, sends desktop
// notifications, and keeps your archive up to date in the background.
package main

import (
	"flag"
	"fmt"
	"os"

	"mu-dl/internal/config"
	"mu-dl/internal/gui"
)

func main() {
	minimized := flag.Bool("minimized", false, "start hidden in the system tray")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *version {
		fmt.Printf("mu-archiver %s\n", gui.Version)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read config: %v (using defaults)\n", err)
	}
	if *minimized {
		cfg.StartMinimized = true
	}
	gui.Run(cfg, cfg.StartMinimized)
}

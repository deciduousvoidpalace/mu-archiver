# MU Archiver / mu-dl
#
#   make            build both binaries for the current machine (x86-64 Linux)
#   make cli        static CLI only (no C toolchain needed)
#   make gui        desktop app (needs the GUI dev packages, see README)
#   make install    install binaries, .desktop entry and icon for this user
#   make test       run the unit tests
#   make dist-linux release tarball in dist/ (Windows/macOS: see packaging/release/)
#
VERSION  := 2.0.0
PREFIX   ?= $(HOME)/.local
BINDIR   := $(PREFIX)/bin
LDFLAGS  := -s -w
GOFLAGS  := -trimpath
GOOS     ?= linux
GOARCH   ?= amd64

.PHONY: all cli gui build linux install uninstall test vet fmt clean icons winres dist-linux deps-debian deps-fedora deps-arch

all: cli gui

cli:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o mu-dl ./cmd/mu-dl

gui:
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o mu-archiver ./cmd/mu-archiver

# compatibility with the v1 Makefile
build: cli
linux: cli
	cp mu-dl mu-dl-linux-amd64

APPID    := org.muarchiver.MUArchiver
ICONDIR  := $(PREFIX)/share/icons/hicolor
ICONSIZES := 32 48 64 128 256 512

install: all
	install -d $(BINDIR) $(PREFIX)/share/applications $(PREFIX)/share/pixmaps
	install -m 0755 mu-dl $(BINDIR)/mu-dl
	install -m 0755 mu-archiver $(BINDIR)/mu-archiver
	for s in $(ICONSIZES); do \
	  install -d $(ICONDIR)/$${s}x$${s}/apps; \
	  install -m 0644 packaging/icons/hicolor/$${s}x$${s}/apps/mu-archiver.png $(ICONDIR)/$${s}x$${s}/apps/mu-archiver.png; \
	done
	install -m 0644 internal/assets/icon.png $(PREFIX)/share/pixmaps/mu-archiver.png
	sed 's#^Exec=mu-archiver#Exec=$(BINDIR)/mu-archiver#; s#^TryExec=mu-archiver#TryExec=$(BINDIR)/mu-archiver#; s#^Icon=.*#Icon=$(ICONDIR)/512x512/apps/mu-archiver.png#' packaging/mu-archiver.desktop > $(PREFIX)/share/applications/$(APPID).desktop
	rm -f $(PREFIX)/share/applications/mu-archiver.desktop
	-update-desktop-database $(PREFIX)/share/applications 2>/dev/null
	-gtk-update-icon-cache -q -t $(ICONDIR) 2>/dev/null
	-xdg-icon-resource forceupdate --theme hicolor 2>/dev/null
	-kbuildsycoca6 --noincremental >/dev/null 2>&1 || kbuildsycoca5 --noincremental >/dev/null 2>&1 || true
	@echo "Installed. Launch 'MU Archiver' from the application menu, or run mu-archiver / mu-dl."

uninstall:
	rm -f $(BINDIR)/mu-dl $(BINDIR)/mu-archiver \
	      $(PREFIX)/share/applications/$(APPID).desktop \
	      $(PREFIX)/share/applications/mu-archiver.desktop \
	      $(PREFIX)/share/pixmaps/mu-archiver.png \
	      $(HOME)/.config/autostart/mu-archiver.desktop
	for s in $(ICONSIZES); do rm -f $(ICONDIR)/$${s}x$${s}/apps/mu-archiver.png; done
	-kbuildsycoca6 --noincremental >/dev/null 2>&1 || kbuildsycoca5 --noincremental >/dev/null 2>&1 || true

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd internal

icons:
	go generate ./internal/assets

# Windows resources (icon, version info, GUI manifest) linked into mu-archiver.exe
# by "go build" for windows/amd64. The .syso is committed; regenerate it after
# changing the icon or VERSION.
winres:
	go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --out cmd/mu-archiver/rsrc --manifest gui \
	  --icon internal/assets/icon.png --product-name "MU Archiver" --file-description "MU Archiver" \
	  --product-version $(VERSION) --file-version $(VERSION) --original-filename mu-archiver.exe --copyright "CC0 1.0 Universal"

# Release tarball for Linux; the GitHub workflow (.github/workflows/release.yml)
# runs this plus the Windows and macOS scripts in packaging/release/.
dist-linux:
	packaging/release/build-linux.sh $(VERSION)

clean:
	rm -rf mu-dl mu-dl-linux-amd64 mu-archiver dist

# Build dependencies for the GUI (OpenGL / X11 / Wayland headers)
deps-debian:
	sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev wayland-protocols

deps-fedora:
	sudo dnf install -y gcc mesa-libGL-devel libX11-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel libXxf86vm-devel libxkbcommon-devel wayland-devel wayland-protocols-devel

deps-arch:
	sudo pacman -S --needed gcc mesa libx11 libxcursor libxrandr libxinerama libxi libxkbcommon wayland wayland-protocols

# MU Archiver

Archive every podcast your **Mysterious Universe** subscription grants —
*Mysterious Universe*, *MU Plus+* and *Inescapable* — in the highest quality
the site offers, across the **entire back-catalogue** (MU Max), with a desktop
app that lives in your system tray and keeps the archive current.

Two front-ends share one engine:

| Binary | What it is |
|---|---|
| `mu-archiver` | Desktop app. Built and tested for **KDE Plasma on x86-64 Linux**; Windows and macOS builds are on the Releases page. Tray icon, notifications, background checks, live rate-limit sliders. |
| `mu-dl` | Command-line archiver. Static binary, no dependencies. Scriptable; has a `watch` mode. |

Both work through the *same personal RSS feeds the site hands to any podcast
app*, so they only ever touch content your subscription already includes.
Downloads are for **personal, offline use** — respect the site's
[Terms of Service](https://mysteriousuniverse.org/) and keep the rate limits
polite.

---

## Highlights

- **Complete catalogue, no guessing.** The dashboard's own season selectors
  are read, so every season your plan unlocks is enumerated exactly
  (with a Max plan that is MU seasons 2–36, Plus+ 1–34, Inescapable — roughly
  1,670 episodes).
- **One interwoven timeline.** MU (Thursdays), MU Plus+ (Mondays) and
  Inescapable are archived together and arranged in release order:
  `2026/2026-08-31 - 34.09 - MU Plus+ - The Endless Day.mp3` sits right before
  `2026/2026-09-03 - 36.09 - MU - The Strange World of Genius Animals.mp3`.
  Downloads run oldest-first in the same order, so the back-catalogue fills in
  chronologically. A classic `<Show>/Season NN/SS.EE - Title.mp3` layout is
  available too; switching moves the files rather than re-downloading.
- **Correct naming.** Episode numbers come from the titles (`36.09 - MU
  Podcast - …`), which is what MU actually uses; the iTunes tags are wrong for
  some shows. The mixed main feed is split per episode into the right show.
- **Self rate-limiting, adjustable live.** Minimum gap between requests,
  bandwidth cap, pause between episodes, parallelism — all with gentle
  defaults, all changeable from the sliders while a run is in progress. HTTP
  429/503 answers trigger an automatic cool-down (honouring `Retry-After`).
- **Resumable and idempotent.** Downloads go to `.part` files, resume with
  HTTP `Range`, are verified against the server's `Content-Length`, and are
  recorded in a manifest so nothing is fetched twice — even when the feed
  reports a wrong file size (it often does). Files from older versions of the
  tool are adopted and renamed automatically.
- **Polite by design.** Feed XML is cached and re-fetched with conditional
  requests; a honest but browser-compatible User-Agent; capped concurrency;
  exponential-backoff retries.
- **Background mode.** Close the window and it keeps running in the tray,
  re-checking for new episodes on a schedule and notifying you when something
  new is archived.

## Download

Prebuilt builds for Linux, Windows and macOS are on the
[Releases page](https://github.com/deciduousvoidpalace/mu-archiver/releases).
Each archive contains the desktop app, the `mu-dl` command-line tool, this
README and the licence.

| Platform | Archive | After unpacking |
|---|---|---|
| Linux x86-64 | `mu-archiver_<version>_linux_amd64.tar.gz` | `./install.sh` installs to `~/.local` and adds the menu entry (`./install.sh --uninstall` removes it again), or just run `./mu-archiver`. Needs glibc 2.34+ (Ubuntu 22.04, Debian 12, Fedora 35 or newer). |
| Windows 10/11 x86-64 | `mu-archiver_<version>_windows_amd64.zip` | Run `mu-archiver.exe`. The build is not code-signed, so SmartScreen may show "Windows protected your PC": *More info → Run anyway*. |
| macOS 11+ (Apple silicon and Intel) | `mu-archiver_<version>_macos_universal.zip` | Drag `MU Archiver.app` to Applications. The app is not notarised: if macOS refuses to open it, use *System Settings → Privacy & Security → Open Anyway*, or run `xattr -dr com.apple.quarantine "/Applications/MU Archiver.app"`. |

The Windows and macOS builds are provided as-is: the tray and notification
integration is tested on KDE Plasma, and the *Launch at login* switch only
works on Linux (XDG autostart). Releases are built by the
[GitHub Actions workflow](.github/workflows/release.yml) from a `v*` tag, and
the `SHA256SUMS` asset lists the archive checksums.

## Build from source

### Build dependencies

The CLI needs only Go 1.24+. The desktop app additionally needs a C compiler
and the OpenGL/X11/Wayland headers:

```bash
make deps-debian   # Ubuntu / Debian / KDE neon / Kubuntu
make deps-fedora   # Fedora
make deps-arch     # Arch / Manjaro
```

### Build and install for your user

```bash
make            # builds ./mu-dl and ./mu-archiver
make install    # ~/.local/bin, .desktop entry, icon → shows up in the KDE menu
```

`make install` puts the binaries in `~/.local/bin`, the launcher in
`~/.local/share/applications/org.muarchiver.MUArchiver.desktop` and the planet
icon in `~/.local/share/icons/hicolor` at every standard size, then refreshes
the KDE menu cache. If the menu entry still shows a generic icon, log out and
back in once (Plasma caches icons per session). `make uninstall` removes it all.

`make cli` builds only the static command-line tool.

## Desktop app

Launch **MU Archiver** from the application menu (or run `mu-archiver`).

1. **Settings** → enter your MU email/password, pick an archive folder, choose
   quality, file layout and shows, and save. Use *Test login* to confirm the account and see
   your plan (e.g. *MU Max+ Yearly*).
2. **Overview** → *Archive now*. The app logs in, reads the dashboard, fetches
   every feed and starts downloading what is missing. Progress, active
   transfers and speed are live; the sliders under *Self rate-limiting* take
   effect immediately.
3. Close the window — the app stays in the **system tray**. The tray menu
   shows status and offers Show / Archive now / Pause / Stop / Check for new /
   Quit. Left-click the tray icon to bring the window back.

Useful settings:

- *Check for new episodes* every N hours (background re-scan + download).
- *Start hidden in the system tray*, *Start archiving on launch*, and
  *Launch at login* (writes `~/.config/autostart/mu-archiver.desktop`).
- *Desktop notifications* for finished runs, new episodes, login problems and
  server cool-downs.
- *Presets*: **Gentle** (3 s gap, 1 download, 15 s between files),
  **Balanced** (default), **Brisk**.

The **Library** page lists the whole catalogue with archive status, filters by
show/status and search, and shows the on-disk path of a selected episode.
**Activity** is the live log.

## Command line

```bash
mu-dl login --email you@example.com --save-credentials   # prompts for the password
mu-dl list                                               # catalogue + status, no download
mu-dl download --out ~/Podcasts/MU                       # archive everything missing
mu-dl watch --every 6h                                   # …then keep checking
```

Flags (all optional; defaults are the polite ones):

| Flag | Meaning |
|---|---|
| `--out <dir>` | Archive folder (default `~/Podcasts/Mysterious Universe`) |
| `--quality hq|sq` | Feed quality (default `hq`) |
| `--layout chronological|seasons` | File layout (default `chronological`, see above) |
| `--shows mu,muplus,inescapable` | Restrict to shows |
| `--season <n>`, `--min-season`, `--max-season` | Season filters |
| `--feed <url>` | Use a feed URL directly (repeatable); skips login |
| `--limit <n>`, `--dry-run`, `--overwrite` | Test runs / re-downloads |
| `--concurrency <n>` | Parallel downloads (default 2) |
| `--request-gap <dur>` | Minimum time between HTTP requests (default 1.5s) |
| `--episode-gap <dur>` | Pause between files per worker (default 5s) |
| `--bandwidth <KiB/s>` | Total download cap (default unlimited) |
| `--every <dur>` | `watch` interval (default 6h) |

`mu-dl config` prints the stored configuration and can set the same values
(`--set-out`, `--set-request-gap`, `--set-bandwidth`, …) or clear things
(`--clear-credentials`, `--clear-session`).

Credentials can also come from `MU_EMAIL` / `MU_PASSWORD`. Ctrl-C stops
cleanly; partial files resume next time.

## How it works

```
login (ASP.NET Identity form, antiforgery token carried over)
  └─ /dashboard  → cards with tokenized feed URLs + season <select>s + plan
       └─ feeds/<track>/<season>/<hq|sq>/<token>   one per season per show
            └─ <enclosure> on libsyn / castos      → Range-resumable MP3
```

- Dedup key: enclosure URL without query string (the main feed overlaps the
  season feeds).
- Ordering: by `<pubDate>` across every feed, oldest first — the download
  queue, the library view and the chronological layout all use it.
- Show detection: title marker (`MU Podcast`, `MU Plus+ Podcast`,
  `Inescapable …`), then enclosure filename prefix (`MU_` vs `MUP_`), then the
  feed track.
- "HQ" means *the highest quality MU hosts for that episode*: recent seasons
  are 320 kbps; the early back-catalogue only exists at lower bitrates and the
  HQ feed serves those files. The full archive is large (hundreds of GB).

## Files

| Path | Purpose |
|---|---|
| `~/.config/mu-dl/config.json` | Settings, credentials (0600) |
| `~/.config/mu-dl/cookies.json` | Login session |
| `~/.config/mu-dl/cache/` | Cached feed XML |
| `<archive>/.mu-archiver/library.json` | Manifest of archived episodes |

## Troubleshooting

- **Login failed** — check the credentials with *Test login*. Two-factor
  authentication is not supported; paste feed URLs with `--feed` instead.
- **"Cooling down"** — the server answered 429/503. The app waits and
  resumes on its own; consider the *Gentle* preset.
- **A file is wrong or truncated** — delete it; the next run detects the
  mismatch and re-downloads it. `--overwrite` forces re-downloads.
- **Wrong or missing icon in the menu / task bar** — run `make install`
  again (it installs all icon sizes and rebuilds the KDE cache), then log out
  and in. The launcher references the icon by absolute path so no icon theme
  is required.
- **No tray icon** — KDE Plasma shows it natively (StatusNotifierItem). Other
  desktops may need a StatusNotifier host (e.g. the GNOME AppIndicator
  extension).

## Development

```bash
make test     # unit tests (parsers, limiter, downloader, engine against a fake site)
make vet
make icons    # regenerate the icon from internal/assets/gen
```

## Disclaimer

Not affiliated with or endorsed by Mysterious Universe / 8th Kind. Use only
with your own account and for content your subscription includes.

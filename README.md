# Hall Clock

Local-only Raspberry Pi hall-clock appliance.

The Pi plugs into a TV/projector over HDMI, runs the hall-clock server locally,
and opens the display in Chromium kiosk mode. Operators control the timer from a
phone on the same Wi-Fi network. Pairing happens from `/pair`; the normal TV
display stays clean and QR-free during meetings.

No cloud service is required during a meeting.

## Architecture

```text
Phone controller -> local Wi-Fi -> Raspberry Pi -> HDMI -> TV/projector
```

The Raspberry Pi runs a single Go binary:

- `/display` full-screen TV clock/countdown
- `/control` mobile operator remote
- `/setup` local device/settings page
- `/api/state` current timer state
- `/api/control/*` start, stop, reset, adjust, bell, and schedule commands
- `/events` Server-Sent Events stream for live display/controller updates
- `/pair` always-available pairing page
- `/qr.png` local QR code for pairing phones to the controller

The app intentionally uses Server-Sent Events instead of WebSockets for the
first version. Displays and controllers only need server-to-client state pushes;
commands are simple local HTTP POST requests. This keeps the Pi runtime smaller
and easier to debug.

Pairing stays available at `/pair` so a printed or bookmarked QR code can always
add a controller phone on the local network.

## Meeting Data

Weekend meetings use a fixed local template:

- Public Talk: 30 minutes
- Watchtower Study: 60 minutes

The app switches to the weekend schedule automatically on Saturday and Sunday.
Monday through Friday use the saved midweek schedule.

Midweek meetings are expected to change weekly. The setup page supports pasting
weekly timing text or a WOL midweek program URL and parsing only the part titles
and minute values for review before saving. Once saved, the normal TV display
does not depend on internet access.

The setup page can also enable automatic weekly import. The server computes the
date-addressable weekly meetings page (`wol.jw.org/<lang>/wol/meetings/<r>/<lib>/<year>/<isoweek>`),
follows its link to the midweek workbook document, and applies the parsed
timings once per ISO week, starting Monday at 3:00 AM in the Pi's local time.
Failures are retried hourly and always keep the last saved schedule, so
meetings still work offline. The language and library
segments of a previously imported URL are reused, so non-English configurations
keep importing in their own language.

Each device can store multiple weekly start times on any day, including
weekends, with several start times per day for halls shared by congregations
that meet at different hours. The automatic pre-meeting countdown uses the
next configured start time for the current day. New installs are seeded with
Monday-Friday evening starts plus Sunday 10:00.

## Recommended Pi Setup

- Raspberry Pi 5, 4GB or 8GB
- Official USB-C power supply
- Case with active cooling
- microSD card or SSD
- Micro-HDMI to HDMI cable
- Raspberry Pi OS with desktop/Chromium

## Project layout

```text
src/hall-clock/        single Go binary + embedded web/ assets
  main.go              flags and startup
  server.go            routing, SSE, snapshots
  handlers.go          HTTP control/config/import handlers
  timer.go             timer + schedule state machine
  schedule.go          midweek/weekend/circuit-overseer schedules
  config.go            config load/save/normalise
  autoimport.go        WOL weekly-timing import
  pairing.go           token, QR, advertised-URL resolution
  model.go             core types
deploy/raspberry-pi/   systemd + Caddy appliance install
deploy/local/          run the same stack on a Mac
scripts/               dev helpers
```

## Development

Common tasks are in the `Makefile` (run `make` to list them):

```sh
make run       # live-reload assets on http://localhost:8480
make test      # go test ./...
make race      # tests with the race detector
make vet       # go vet ./...
make build     # ./hall-clock
make build-pi  # dist/hall-clock-arm64 for the Pi
```

`make run` serves the web assets straight from disk (`-web-dir`), so edits to
HTML/CSS/JS show up on a browser refresh without a rebuild.

Open:

- Controller: http://localhost:8480 (also `/control`)
- Display: http://localhost:8480/display
- Pairing: http://localhost:8480/pair
- Setup: http://localhost:8480/setup

## Raspberry Pi Deployment

See [deploy/raspberry-pi/README.md](deploy/raspberry-pi/README.md).

The appliance runs the Go app on a Unix socket behind Caddy on port 80, so
phones use a clean URL such as `http://hallclock.local` with no port. On a Mac
you can preview the same stack — see [deploy/local/README.md](deploy/local/README.md).

## License

[MIT](LICENSE) © Daniel Ehoneah

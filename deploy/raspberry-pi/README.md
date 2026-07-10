# Raspberry Pi Deployment

This setup runs Hall Clock as a local-only appliance:

```text
Phone controller -> local Wi-Fi -> Raspberry Pi -> HDMI -> TV/projector
```

The Pi starts the Go server with `systemd` and opens Chromium in kiosk mode on
boot.

## Pi Prerequisites

- Raspberry Pi OS with desktop
- Chromium installed
- Wi-Fi or Ethernet connected to the same network as controller phones
- Hostname set to `hallclock` for `http://hallclock.local`

Set the hostname:

```sh
sudo raspi-config
```

Choose `System Options` -> `Hostname` -> `hallclock`, then reboot.

## Build Binary

From a development machine:

```sh
make build-pi           # arm64, into dist/
make build-pi-armv7     # 32-bit Pi OS
```

Copy the binary to the Pi:

```sh
scp dist/hall-clock-linux-arm64 pi@hallclock.local:/tmp/hall-clock
```

This is only needed for the *first* install. After that the Pi updates itself
from GitHub releases — see "Updates".

## Install On Pi

Copy this deploy folder to the Pi, then run:

```sh
sudo ./install.sh
```

The installer:

- installs `/opt/hall-clock/hall-clock`
- creates `/etc/hall-clock/config.json` on first run
- installs `caddy` with `apt` when missing
- installs `hall-clock.service`
- installs `hall-clock-kiosk.service`
- installs `/etc/caddy/Caddyfile`
- enables both services
- runs the Go app on a Unix socket behind Caddy on port `80` (see "Zero-port
  Unix socket")
- adds the `caddy` user to the `pi` group so Caddy can reach that socket
- serves plain HTTP for `hallclock.local` so bring-your-own phones never hit a
  certificate warning (see "Why not HTTPS?" below)
- installs and enables `hall-clock-update.timer` (nightly check) and
  `hall-clock-update.path` (Update button), plus the two update units
  (see "Updates")
- installs and enables `hall-clock-housekeeping.timer` to clean kiosk/browser
  caches and keep journals bounded

## Updates

**Nothing installs itself.** A Pi checks nightly whether a release is waiting and
records the answer; installing restarts the app, and that only ever happens
because a person asked for it. Shipping a new version still needs no ssh, VPN, or
physical access — someone at the hall taps a button.

Tag a release:

```sh
git tag v1.2.3 && git push origin v1.2.3
```

`.github/workflows/release.yml` vets, tests, cross-compiles the three Pi
architectures (arm64, armv7, armv6), packages the Raspberry Pi deploy files, and
publishes them to a GitHub Release with a `SHA256SUMS` file. The tag becomes the
version the binary reports (`hall-clock -version`).

### Checking (automatic, nightly)

`hall-clock-update.timer` runs `hall-clock-update.sh --check` at 4:00 AM, plus a
random delay of up to 30 minutes so halls do not all hit the GitHub API at once.
It compares `hall-clock -version` against the latest release tag and writes the
answer to `/var/lib/hall-clock/update-status.json`. It downloads nothing,
installs nothing, and restarts nothing. It needs only *outbound* internet, which
the Pi already has for the WOL auto-import.

The setup page reads that file, which is how you can tell from another hall
whether a Pi is stuck — a box whose Wi-Fi has been down for three weeks otherwise
looks identical to one that is up to date.

### Installing (only when a person asks)

Two ways, both running the same `hall-clock-update.sh` as root:

- the **Update button** on `/setup` (see below)
- `sudo systemctl start hall-clock-update.service` over ssh

Either way the updater:

- compares `hall-clock -version` against the latest release tag, and stops if
  they match
- **refuses to update during a meeting.** A restart rebuilds state from config
  with the timer reset to idle, so updating mid-meeting would blank a running
  countdown on the projector. It checks `/api/state` and refuses if the status is
  not `idle`.
- downloads the binary matching this Pi's CPU (`uname -m`) and the Raspberry Pi
  deploy bundle
- verifies both downloads against `SHA256SUMS` before installing them
- installs by `rename()` within `/opt/hall-clock`, which is atomic — there is no
  moment where the binary is half-written
- refreshes the systemd units, Caddyfile, kiosk script, updater script, and
  housekeeping script from the deploy bundle
- keeps the old binary as `hall-clock.previous` and **rolls back** if the new
  one fails to restart or does not answer on its socket within 15 seconds

### The Update button

The Software card on `/setup` shows the running version, whether a newer release
exists, and when the check last ran. When an update is available and the timer is
idle, tapping **Update** installs it there and then. Since nothing installs
automatically, this is the normal way a hall gets a new version — and the only
way for a hall on a LAN you cannot reach: talk someone through opening the setup
page from a paired phone and tapping one button.

The button is disabled while a meeting is running or paused, because installing
restarts the app and a restart resets the countdown. Reset the timer to idle
first. During the update the page shows `Downloading… → Restarting…`; the app
goes away for a second or two while it restarts, and the page reconnects on its
own and reports the result.

How the button reaches root, given the app runs as `pi` with
`NoNewPrivileges=true` (so `sudo` cannot work):

- the app creates `/var/lib/hall-clock/update-requested`, a file in its own
  `StateDirectory`
- `hall-clock-update.path` watches for that file and starts the root-owned
  `hall-clock-update.service`
- the updater deletes the trigger as its first action, then reports progress by
  writing `/var/lib/hall-clock/update-status.json`, which the app reads back

That is the entire privilege boundary: the app can ask for an update, and can do
nothing else as root. `POST /api/update` requires the pairing token; the update
runs only when the timer is idle.

`/var/lib/hall-clock` is a `StateDirectory` rather than the `RuntimeDirectory`
used for the socket, because systemd deletes the latter on restart — which is
precisely when the status of an update matters.

Useful commands:

```sh
systemctl list-timers hall-clock-update.timer         # when it next checks
sudo systemctl start hall-clock-update-check.service  # check now, install nothing
sudo systemctl start hall-clock-update.service        # install now
journalctl -u hall-clock-update.service               # what it did
cat /var/lib/hall-clock/update-status.json            # what the setup page shows
/opt/hall-clock/hall-clock -version                   # what is running
```

## Housekeeping

`hall-clock-housekeeping.timer` runs daily around 4:45 AM. It does not appear in
the web UI. It removes known Chromium kiosk/cache directories, old wall-clock
kiosk state, any abandoned `/opt/hall-clock/.update.*` staging directories,
vacuum systemd journals to 100M, and runs `apt-get clean`.

Useful commands:

```sh
systemctl list-timers hall-clock-housekeeping.timer
sudo systemctl start hall-clock-housekeeping.service
journalctl -u hall-clock-housekeeping.service
```

To track a fork instead of the upstream repo, set the repo in
`/etc/hall-clock/update.env`:

```sh
REPO="your-org/your-fork"
```

To stop a hall from even checking, disable the timer:
`sudo systemctl disable --now hall-clock-update.timer`. To take away the Update
button's ability to install, disable the watcher:
`sudo systemctl disable --now hall-clock-update.path`.

Older installs whose updater predates the deploy bundle still update only the
binary. Bootstrap those once by re-running `install.sh` or manually installing
the current `hall-clock-update.sh`; after that, future updates refresh the
binary and deploy files together.

Note that the updater does **not** verify a signature, only a checksum. The
checksum is served by GitHub over TLS alongside the binary, so it defends
against a corrupted download, not against someone who controls the release. If
these Pis matter enough, sign the artifacts with `cosign` or `minisign` and
check the signature in `hall-clock-update.sh` before installing.

## Controller URL (no port)

The pairing QR code and controller link advertise `http://hallclock.local`
(no `:8480`). This comes from the app's `-public-url http://hallclock.local`
flag in `hall-clock.service` — the app never guesses its own public address.

If you leave `-public-url` unset, the app instead derives the address from the
reverse proxy: it trusts the `X-Forwarded-Host` / `X-Forwarded-Proto` headers
Caddy sends, but only when the request comes from loopback (Caddy itself), so a
phone cannot poison the pairing link. Either way you get `http://hallclock.local`
with no port, as long as you open the pairing page through Caddy on port 80
(`http://hallclock.local/pair`). On the Pi the app has no TCP port at all — see
"Zero-port Unix socket".

The in-app Setup field "Advertised controller URL" is a last-resort manual
override (under "Advanced") — normally leave it blank.

## Zero-port Unix socket

On the Pi the app does not open a TCP port. It listens on a Unix socket at
`/run/hall-clock/app.sock`, and Caddy proxies to it (`reverse_proxy
unix//run/hall-clock/app.sock`). Benefits:

- **No port to conflict with** anything else on the box.
- **Not reachable over the network** — not even `127.0.0.1`. Access is gated by
  filesystem permissions, so only Caddy (and root) can talk to the app; a
  localhost TCP port, by contrast, is reachable by any local process.

How the permissions line up:

- `hall-clock.service` uses `RuntimeDirectory=hall-clock`, so systemd creates
  `/run/hall-clock` owned `pi:pi` (mode `0750`) and cleans it up on stop.
- The app creates the socket and `chmod`s it to `0660` (owner + group only).
- The installer runs `usermod -aG pi caddy` so the `caddy` user is in the `pi`
  group and can connect. (Group membership is read at process start, so Caddy
  is restarted after.)

Troubleshooting a `502` from Caddy:

- App up? `systemctl status hall-clock` and `ls -l /run/hall-clock/app.sock`.
- Caddy in the group? `id caddy` should list `pi`. If you added it after Caddy
  started, `sudo systemctl restart caddy`.
- Test the socket directly: `sudo -u caddy curl --unix-socket
  /run/hall-clock/app.sock http://localhost/api/state`.

Local development still uses a normal TCP port — `-addr` defaults to `:8480`, so
`go run ./src/hall-clock` serves `http://localhost:8480`. The socket is only the
Pi/production setup. Pass `-addr unix:/path/to.sock` to use a socket elsewhere.

## Permanent printed QR code

You can print one QR code and leave it posted in the hall forever. The
controller page **auto-pairs**: when it loads without a token it fetches one
from the (LAN-open) `/api/pairing` endpoint. The controller is also served at
the site root, so the QR can point at the cleanest possible tokenless URL,
which never changes:

```
http://hallclock.local
```

Generate that QR with any QR tool and print it. On first scan the phone lands
on the controller and pairs itself — no token, no `/control`, nothing to
reprint if the token ever changes.

Notes:
- Plain HTTP is deliberate: HTTPS on a `.local` name always triggers a
  certificate warning on phones (see "Why not HTTPS?"). Plain HTTP shows only a
  quiet "Not secure" label, which is fine on a trusted LAN.
- `hallclock.local` relies on mDNS. iOS resolves it; most modern Android phones
  do too, but test on a real Android phone first. If one can't resolve `.local`,
  print a QR of the Pi's reserved-DHCP IP instead (e.g. `http://192.168.1.50`) —
  which works precisely because there's no cert to name-match.
- This works because pairing is intentionally open on the LAN. Keep the device
  on a trusted network and never expose it to the internet — a permanent QR
  plus internet exposure would mean permanent public control.

## Why not HTTPS?

There is **no warning-free HTTPS on a `.local` name**, so this appliance uses
plain HTTP on purpose:

- Public CAs (Let's Encrypt, etc.) cannot issue certificates for `.local`.
- The only HTTPS option for `.local` is a self-signed / internal CA, which every
  phone distrusts → a full "Your connection is not private" error unless that
  phone has manually installed the CA. For bring-your-own member phones that is
  a worse experience than plain HTTP's quiet "Not secure" label.

The security boundary here is the **trusted LAN**, not the transport. Keep the
device on an isolated network and never expose it to the internet.

If you ever need a real padlock on member phones, the only way is a **real
domain** with a Let's Encrypt certificate via the DNS-01 challenge (no inbound
exposure) — that also sidesteps `.local` resolution quirks. That requires a
domain you control and is out of scope for this `.local` setup.

## URLs

- Controller: `http://hallclock.local` (also `/control`)
- Display: `http://hallclock.local/display`
- Pairing: `http://hallclock.local/pair`
- Setup: `http://hallclock.local/setup`

## Normal Use

1. Pi boots and opens the clean TV display.
2. Operator visits `/pair` once to pair a phone.
3. Phone stores the local control token.
4. Normal meetings use the saved controller bookmark.

The QR code is intentionally not shown on the main display during meetings.
The display switches to the fixed weekend schedule automatically on Saturday
and Sunday; Monday through Friday use the saved/imported midweek schedule.
When automatic midweek import is enabled, it runs Monday at 3:00 AM in the Pi's
local time. If the import fails, the existing schedule is kept and the Pi retries
hourly until the current week imports successfully.

## Service Commands

```sh
sudo systemctl status hall-clock
sudo systemctl restart hall-clock
sudo journalctl -u hall-clock -f

sudo systemctl status hall-clock-kiosk
sudo systemctl restart hall-clock-kiosk
```

## Updating

Copy a new binary to `/tmp/hall-clock`, then:

```sh
sudo install -m 0755 /tmp/hall-clock /opt/hall-clock/hall-clock
sudo systemctl restart hall-clock
```

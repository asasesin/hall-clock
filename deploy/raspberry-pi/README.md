# Raspberry Pi Deployment

This setup runs Wall Clock as a local-only appliance:

```text
Phone controller -> local Wi-Fi -> Raspberry Pi -> HDMI -> TV/projector
```

The Pi starts the Go server with `systemd` and opens Chromium in kiosk mode on
boot.

## Pi Prerequisites

- Raspberry Pi OS with desktop
- Chromium installed
- Wi-Fi or Ethernet connected to the same network as controller phones
- Hostname set to `wallclock` for `http://wallclock.local:8080`

Set the hostname:

```sh
sudo raspi-config
```

Choose `System Options` -> `Hostname` -> `wallclock`, then reboot.

## Build Binary

From a development machine:

```sh
GOOS=linux GOARCH=arm64 go build -o wall-clock ./cmd/wall-clock
```

Copy the binary to the Pi:

```sh
scp wall-clock pi@wallclock.local:/tmp/wall-clock
```

## Install On Pi

Copy this deploy folder to the Pi, then run:

```sh
sudo ./install.sh
```

The installer:

- installs `/opt/wall-clock/wall-clock`
- creates `/etc/wall-clock/config.json` on first run
- installs `wall-clock.service`
- installs `wall-clock-kiosk.service`
- enables both services
- configures QR/controller links to use `http://wallclock.local:8080/control`

## URLs

- Display: `http://wallclock.local:8080/display`
- Pairing: `http://wallclock.local:8080/pair`
- Controller: `http://wallclock.local:8080/control`
- Setup: `http://wallclock.local:8080/setup`

## Normal Use

1. Pi boots and opens the clean TV display.
2. Operator visits `/pair` once to pair a phone.
3. Phone stores the local control token.
4. Normal meetings use the saved controller bookmark.

The QR code is intentionally not shown on the main display during meetings.
When automatic midweek import is enabled, it runs Monday at 3:00 AM in the Pi's
local time. If the import fails, the existing schedule is kept and the Pi retries
hourly until the current week imports successfully.

## Service Commands

```sh
sudo systemctl status wall-clock
sudo systemctl restart wall-clock
sudo journalctl -u wall-clock -f

sudo systemctl status wall-clock-kiosk
sudo systemctl restart wall-clock-kiosk
```

## Updating

Copy a new binary to `/tmp/wall-clock`, then:

```sh
sudo install -m 0755 /tmp/wall-clock /opt/wall-clock/wall-clock
sudo systemctl restart wall-clock
```

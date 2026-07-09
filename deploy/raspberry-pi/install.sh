#!/usr/bin/env sh
set -eu

APP_DIR="/opt/hall-clock"
CONFIG_DIR="/etc/hall-clock"
BIN_SRC="${1:-/tmp/hall-clock}"
BIN_DST="${APP_DIR}/hall-clock"
UNIT_DIR="/etc/systemd/system"
CADDY_DIR="/etc/caddy"

ensure_caddy() {
  if command -v caddy >/dev/null 2>&1 && [ -d "$CADDY_DIR" ]; then
    return 0
  fi

  echo "Caddy not found; installing with apt..."
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y caddy
}

if [ ! -f "$BIN_SRC" ]; then
  echo "Missing binary at $BIN_SRC"
  echo "Build and copy it first, or pass the binary path: sudo ./install.sh /path/to/hall-clock"
  exit 1
fi

ensure_caddy

if ! systemctl list-unit-files caddy.service >/dev/null 2>&1; then
  echo "Caddy service not found."
  echo "Caddy installed but no systemd service was found."
  exit 1
fi

install -d -m 0755 "$APP_DIR"
install -d -m 0755 "$CONFIG_DIR"
install -m 0755 "$BIN_SRC" "$BIN_DST"
install -m 0644 hall-clock.service "$UNIT_DIR/hall-clock.service"
install -m 0644 hall-clock-kiosk.service "$UNIT_DIR/hall-clock-kiosk.service"
install -m 0644 hall-clock-update.service "$UNIT_DIR/hall-clock-update.service"
install -m 0644 hall-clock-update-check.service "$UNIT_DIR/hall-clock-update-check.service"
install -m 0644 hall-clock-update.timer "$UNIT_DIR/hall-clock-update.timer"
install -m 0644 hall-clock-update.path "$UNIT_DIR/hall-clock-update.path"
install -m 0755 hall-clock-kiosk.sh "$APP_DIR/hall-clock-kiosk.sh"
install -m 0755 hall-clock-update.sh "$APP_DIR/hall-clock-update.sh"
install -m 0644 Caddyfile "$CADDY_DIR/Caddyfile"
chown -R pi:pi "$APP_DIR" "$CONFIG_DIR"

# The app listens on a Unix socket owned by pi:pi (mode 0660). Add the caddy
# user to the pi group so the reverse proxy can connect to it. (Restarting
# caddy below picks up the new group membership.)
if id caddy >/dev/null 2>&1; then
  usermod -aG pi caddy
fi

systemctl daemon-reload
# Clean up units and kiosk state from the pre-rename wall-clock install. Leaving
# the old Chromium kiosk running can fill /home with browser metrics/cache data.
systemctl disable --now wall-clock.service wall-clock-kiosk.service 2>/dev/null || true
rm -rf /home/pi/.config/wall-clock-kiosk
systemctl enable hall-clock.service
systemctl enable hall-clock-kiosk.service
systemctl enable hall-clock-update.timer
systemctl enable hall-clock-update.path
systemctl restart hall-clock.service
systemctl restart hall-clock-kiosk.service
systemctl restart hall-clock-update.timer
systemctl restart hall-clock-update.path
systemctl restart caddy.service

echo "Hall Clock installed."
echo "Version: $("$BIN_DST" -version)"
echo "Display: http://hallclock.local/display"
echo "Pair:    http://hallclock.local/pair"
echo "Updates: checked nightly; installed from Setup > Software, or with"
echo "         sudo systemctl start hall-clock-update.service"

#!/usr/bin/env sh
set -eu

APP_DIR="/opt/hall-clock"
CONFIG_DIR="/etc/hall-clock"
BIN_SRC="${1:-/tmp/hall-clock}"
BIN_DST="${APP_DIR}/hall-clock"
UNIT_DIR="/etc/systemd/system"
CADDY_DIR="/etc/caddy"

if [ ! -f "$BIN_SRC" ]; then
  echo "Missing binary at $BIN_SRC"
  echo "Build and copy it first, or pass the binary path: sudo ./install.sh /path/to/hall-clock"
  exit 1
fi

if [ ! -d "$CADDY_DIR" ]; then
  echo "Missing $CADDY_DIR"
  echo "Install Caddy before running this installer."
  exit 1
fi

if ! systemctl list-unit-files caddy.service >/dev/null 2>&1; then
  echo "Caddy service not found."
  echo "Install and enable Caddy before running this installer."
  exit 1
fi

install -d -m 0755 "$APP_DIR"
install -d -m 0755 "$CONFIG_DIR"
install -m 0755 "$BIN_SRC" "$BIN_DST"
install -m 0644 hall-clock.service "$UNIT_DIR/hall-clock.service"
install -m 0644 hall-clock-kiosk.service "$UNIT_DIR/hall-clock-kiosk.service"
install -m 0755 hall-clock-kiosk.sh "$APP_DIR/hall-clock-kiosk.sh"
install -m 0644 Caddyfile "$CADDY_DIR/Caddyfile"
chown -R pi:pi "$APP_DIR" "$CONFIG_DIR"

# The app listens on a Unix socket owned by pi:pi (mode 0660). Add the caddy
# user to the pi group so the reverse proxy can connect to it. (Restarting
# caddy below picks up the new group membership.)
if id caddy >/dev/null 2>&1; then
  usermod -aG pi caddy
fi

systemctl daemon-reload
systemctl enable hall-clock.service
systemctl enable hall-clock-kiosk.service
systemctl restart hall-clock.service
systemctl restart hall-clock-kiosk.service
systemctl restart caddy.service

echo "Hall Clock installed."
echo "Display: http://hallclock.local/display"
echo "Pair:    http://hallclock.local/pair"

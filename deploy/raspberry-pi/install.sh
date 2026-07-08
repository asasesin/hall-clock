#!/usr/bin/env sh
set -eu

APP_DIR="/opt/wall-clock"
CONFIG_DIR="/etc/wall-clock"
BIN_SRC="${1:-/tmp/wall-clock}"
BIN_DST="${APP_DIR}/wall-clock"
UNIT_DIR="/etc/systemd/system"

if [ ! -f "$BIN_SRC" ]; then
  echo "Missing binary at $BIN_SRC"
  echo "Build and copy it first, or pass the binary path: sudo ./install.sh /path/to/wall-clock"
  exit 1
fi

install -d -m 0755 "$APP_DIR"
install -d -m 0755 "$CONFIG_DIR"
install -m 0755 "$BIN_SRC" "$BIN_DST"
install -m 0644 wall-clock.service "$UNIT_DIR/wall-clock.service"
install -m 0644 wall-clock-kiosk.service "$UNIT_DIR/wall-clock-kiosk.service"
install -m 0755 wall-clock-kiosk.sh "$APP_DIR/wall-clock-kiosk.sh"
chown -R pi:pi "$APP_DIR" "$CONFIG_DIR"

systemctl daemon-reload
systemctl enable wall-clock.service
systemctl enable wall-clock-kiosk.service
systemctl restart wall-clock.service
systemctl restart wall-clock-kiosk.service

echo "Wall Clock installed."
echo "Display: http://wallclock.local:8080/display"
echo "Pair:    http://wallclock.local:8080/pair"

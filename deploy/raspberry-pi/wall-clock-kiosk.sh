#!/usr/bin/env sh
set -eu

xset s off || true
xset -dpms || true
xset s noblank || true

while ! curl -fsS http://127.0.0.1:8080/api/state >/dev/null 2>&1; do
  sleep 1
done

if command -v chromium-browser >/dev/null 2>&1; then
  CHROMIUM=chromium-browser
else
  CHROMIUM=chromium
fi

PROFILE_DIR="${HOME}/.config/wall-clock-kiosk/chromium"
mkdir -p "$PROFILE_DIR"

exec "$CHROMIUM" \
  --kiosk \
  --user-data-dir="$PROFILE_DIR" \
  --no-first-run \
  --noerrdialogs \
  --disable-infobars \
  --disable-session-crashed-bubble \
  --check-for-update-interval=31536000 \
  --autoplay-policy=no-user-gesture-required \
  http://127.0.0.1:8080/display

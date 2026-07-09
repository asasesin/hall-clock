#!/usr/bin/env sh
set -eu

xset s off || true
xset -dpms || true
xset s noblank || true

# The app listens on a Unix socket, so the kiosk reaches it through Caddy. Use
# localhost (not hallclock.local) so the local display never depends on the Pi
# resolving its own .local name. Waiting on this URL confirms app + Caddy are up.
BASE_URL="http://localhost"

while ! curl -fsS "$BASE_URL/api/state" >/dev/null 2>&1; do
  sleep 1
done

if command -v chromium-browser >/dev/null 2>&1; then
  CHROMIUM=chromium-browser
else
  CHROMIUM=chromium
fi

PROFILE_DIR="${HOME}/.config/hall-clock-kiosk/chromium"
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
  "$BASE_URL/display"

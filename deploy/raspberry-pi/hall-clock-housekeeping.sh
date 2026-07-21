#!/usr/bin/env sh
set -eu

HOME_DIR="/home/pi"

log() { echo "hall-clock-housekeeping: $*"; }

log "cleaning kiosk/browser caches"
rm -rf \
  "$HOME_DIR/.config/wall-clock-kiosk" \
  "$HOME_DIR/.config/chromium/DeferredBrowserMetrics" \
  "$HOME_DIR/.config/hall-clock-kiosk/chromium/DeferredBrowserMetrics" \
  "$HOME_DIR/.config/hall-clock-kiosk/chromium/component_crx_cache" \
  "$HOME_DIR/.config/hall-clock-kiosk/chromium/extensions_crx_cache" \
  "$HOME_DIR/.config/hall-clock-kiosk/chromium/OnDeviceHeadSuggestModel" \
  "$HOME_DIR/.config/hall-clock-kiosk/chromium/WasmTtsEngine"

# Leftover updater staging directories — but only stale ones. This timer and
# the updater share no lock, and a Persistent= catch-up run right after a
# morning power-on must not delete the staging area of an update someone just
# started from the setup page.
find /opt/hall-clock -maxdepth 1 -name '.update.*' -mmin +120 -exec rm -rf {} + 2>/dev/null || true

log "vacuuming journal to 100M"
journalctl --vacuum-size=100M >/dev/null

if command -v apt-get >/dev/null 2>&1; then
  log "cleaning apt package cache"
  apt-get clean
fi

log "disk after cleanup: $(df -h / | awk 'NR==2 {print $5 " used, " $4 " free"}')"

# Local plain-HTTP preview (Mac / development)

Run the app behind Caddy on your Mac to preview the portless
`http://hallclock.local` URL exactly as phones see it on the Pi — same plain
HTTP, no certificates.

```sh
brew install caddy                       # once

# terminal 1 — the app (local dev runs on TCP :8480)
cd src/hall-clock && go run . -web-dir web

# terminal 2 — Caddy in front (binds port 80, needs sudo)
sudo caddy run --config deploy/local/Caddyfile
```

Open **http://hallclock.local** (or **http://localhost**).

For `hallclock.local` to resolve on this Mac, set it up first — either:

```sh
sudo scutil --set LocalHostName hallclock          # Bonjour, LAN-wide
# or: echo "127.0.0.1 hallclock.local" | sudo tee -a /etc/hosts
```

You do **not** need Caddy just to click around — the app already serves
everything directly at **http://localhost:8480**. Use this only to preview the
portless URL the way phones will hit it.

Note: this proxies to the app on TCP `:8480`. The Pi instead uses a Unix socket
(`reverse_proxy unix//run/hall-clock/app.sock`) — see
[`../raspberry-pi/`](../raspberry-pi/) for the real appliance deployment.

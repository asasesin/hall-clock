package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// PIN pairing
// -----------
// A control token runs the whole meeting, so handing one to any browser that
// asks makes "paired" mean nothing more than "on the wifi". Pairing instead
// asks a phone to prove somebody responsible for the hall let it in: it quotes
// the PIN the congregation set in /setup.
//
// The PIN is stored as typed, so /setup can show the hall which PIN is current
// — a shared secret nobody can look up is one that gets forgotten and reset
// every few months. Hashing it would buy less than it appears to: ControlToken
// sits in the same file in the clear and is strictly more powerful, so anyone
// who can read config.json already owns the clock. The real cost is PIN reuse
// — treat this PIN as readable by whoever can read the SD card, and do not
// reuse one that unlocks anything else.

const (
	// minPINLength keeps a PIN clear of instantly-guessable territory without
	// making it something nobody can read off a card in a dim hall. Six or more
	// is worth recommending; four is the floor, not the advice.
	minPINLength = 4
	maxPINLength = 32
)

// maxPINFailures and pinLockout throttle guessing. A PIN is long-lived, so it
// has to survive somebody grinding at it all evening: five wrong tries buy a
// five-minute pause, which turns even a four-digit space into days of work.
const (
	maxPINFailures = 5
	pinLockout     = 5 * time.Minute
)

// pairingGrace is the window opened at startup when no PIN has been set yet, so
// the person installing the appliance can pair a phone and go set one. It is
// the bootstrap, and it closes on its own.
const pairingGrace = 15 * time.Minute

// pairingWindow is how long "add a phone" stays open when an already-paired
// controller starts it, so a second phone can join without being told the PIN.
const pairingWindow = 5 * time.Minute

// normalizePIN trims and length-checks an operator-supplied PIN. Digits are the
// expected shape but nothing enforces that: a hall that prefers a word should
// get a word.
func normalizePIN(pin string) (string, error) {
	pin = strings.TrimSpace(pin)
	if n := len([]rune(pin)); n < minPINLength || n > maxPINLength {
		return "", fmt.Errorf("PIN must be between %d and %d characters", minPINLength, maxPINLength)
	}
	return pin, nil
}

// pinMatches compares a candidate against the stored PIN in constant time, so
// how fast a guess is rejected says nothing about how much of it was right.
func pinMatches(expected string, pin string) bool {
	if expected == "" || pin == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(pin)) == 1
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	host := ""

	// X-Forwarded-* is only trustworthy when the immediate peer is our own
	// reverse proxy (loopback). Honouring it from an arbitrary client would
	// let a phone poison the pairing/QR link via header injection.
	if isTrustedProxyRequest(r) {
		if proto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
			scheme = strings.ToLower(proto)
		}
		host = firstForwardedValue(r.Header.Get("X-Forwarded-Host"))
	} else if r.TLS != nil {
		scheme = "https"
	}

	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = displayHost(":8480")
	} else {
		host = networkReachableHost(host)
	}
	return scheme + "://" + host
}

// isTrustedProxyRequest reports whether the request's immediate peer is
// loopback, i.e. our co-located reverse proxy. Only then are X-Forwarded-*
// headers safe to trust.
func isTrustedProxyRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// firstForwardedValue returns the first entry of a possibly comma-separated
// X-Forwarded-* header value (proxies may append a list).
func firstForwardedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if i := strings.IndexByte(value, ','); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

func networkReachableHost(hostport string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
		port = ""
	}

	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]" {
		host = firstLANIP()
	}

	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func withToken(target string, token string) string {
	if strings.Contains(target, "?") {
		return target + "&token=" + token
	}
	return target + "?token=" + token
}

func displayHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = firstLANIP()
	}
	return net.JoinHostPort(host, port)
}

func normalizeAdvertisedControlURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid advertised URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("invalid advertised URL")
	}
	// The controller is served at the site root, so a bare host advertises the
	// cleanest URL (http://hallclock.local). A lone "/" collapses to bare; an
	// explicit path like "/control" is respected as an override.
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String(), nil
}

func shouldUseConfiguredAdvertisedURL(target string, r *http.Request) bool {
	parsedTarget, err := url.Parse(target)
	if err != nil {
		return false
	}
	configuredHost := strings.TrimSpace(parsedTarget.Hostname())
	if configuredHost == "" {
		return false
	}

	requestHost := strings.TrimSpace(r.Host)
	if requestHost == "" {
		return true
	}
	// A Host behind a proxy on a standard port (80/443) has no ":port", and
	// net.SplitHostPort returns an empty host + error for it — so only adopt
	// its result on success, otherwise keep the original (bracket-stripped) host.
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = host
	} else {
		requestHost = strings.Trim(requestHost, "[]")
	}
	requestHost = strings.TrimSpace(requestHost)

	// Local development commonly opens the app from localhost while a stale
	// appliance hostname remains saved in config. In that case, prefer the
	// current reachable request host so QR pairing still works for phones on
	// the LAN. This used to match the literal "hallclock.local" only, which let
	// a renamed Pi — or a hostname typo'd once into the setup page — slip
	// through; that matters more now the config value outranks -public-url and
	// nothing downstream second-guesses it.
	if isLoopbackHost(requestHost) && !isLoopbackHost(configuredHost) {
		return false
	}

	return true
}

// isLoopbackHost reports whether a bare host (already stripped of any port and
// IPv6 brackets) names the machine serving the request.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func advertisedControlURL(cliURL string, configuredURL string, r *http.Request) string {
	// The saved setup-page URL outranks the -public-url flag: the flag is the
	// shipped default baked into the systemd unit (http://<host>.local), while
	// the config value is an operator's explicit runtime choice — e.g. an HTTPS
	// domain that survives updates, which the unit's flag would otherwise mask
	// forever.
	target, err := normalizeAdvertisedControlURL(configuredURL)
	if err == nil && target != "" && shouldUseConfiguredAdvertisedURL(target, r) {
		return target
	}
	target, err = normalizeAdvertisedControlURL(cliURL)
	if err == nil && target != "" {
		return target
	}
	return requestBaseURL(r)
}

func firstLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "localhost"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host + ".local"
	}
	return "localhost"
}

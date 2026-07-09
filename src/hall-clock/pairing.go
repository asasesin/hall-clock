package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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
	// appliance hostname such as hallclock.local remains saved in config. In
	// that case, prefer the current reachable request host so QR pairing still
	// works for phones on the LAN.
	if configuredHost == "hallclock.local" &&
		(requestHost == "localhost" || requestHost == "127.0.0.1" || requestHost == "::1") {
		return false
	}

	return true
}

func advertisedControlURL(cliURL string, configuredURL string, r *http.Request) string {
	target, err := normalizeAdvertisedControlURL(cliURL)
	if err == nil && target != "" {
		return target
	}
	target, err = normalizeAdvertisedControlURL(configuredURL)
	if err == nil && target != "" && shouldUseConfiguredAdvertisedURL(target, r) {
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

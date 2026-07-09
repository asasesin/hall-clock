package main

import (
	"embed"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
)

//go:embed web
var webFS embed.FS

func main() {
	var addr string
	var publicURL string
	var configPath string
	var webDir string

	flag.StringVar(&addr, "addr", ":8480", "listen address")
	flag.StringVar(&publicURL, "public-url", "", "controller URL for QR codes")
	flag.StringVar(&configPath, "config", defaultConfigPath(), "path to JSON config file")
	flag.StringVar(&webDir, "web-dir", "", "serve web assets live from this directory instead of the embedded copy (dev)")
	flag.Parse()

	srv, err := newServer(configPath)
	if err != nil {
		log.Fatal(err)
	}
	if webDir != "" {
		info, statErr := os.Stat(webDir)
		if statErr != nil || !info.IsDir() {
			log.Fatalf("-web-dir %q is not a directory: %v", webDir, statErr)
		}
		srv.webAssets = os.DirFS(webDir)
		log.Printf("serving web assets live from %s (dev mode)", webDir)
	}
	mux, err := srv.routes(publicURL)
	if err != nil {
		log.Fatal(err)
	}

	go srv.autoImportLoop()

	log.Printf("hall-clock listening on %s", addr)
	if !strings.HasPrefix(addr, "unix:") {
		log.Printf("display: http://%s/display", displayHost(addr))
		log.Printf("control: http://%s/control", displayHost(addr))
	}
	log.Printf("config: %s", configPath)

	if err := serve(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// serve listens on addr and serves handler. addr is either a TCP address
// (":8480", "127.0.0.1:8480") or "unix:/path/to.sock" for a Unix domain
// socket. The socket form gives the app no TCP port at all — it is reachable
// only by processes with filesystem access to the socket (a co-located reverse
// proxy whose user shares the socket's group), never over the network.
func serve(addr string, handler http.Handler) error {
	path, isUnix := strings.CutPrefix(addr, "unix:")
	if !isUnix {
		return http.ListenAndServe(addr, handler)
	}
	// Clear a stale socket left by an unclean shutdown, then listen.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Restrict to owner + group (the proxy user must be in that group); no
	// access for other local users, and none over the network.
	if err := os.Chmod(path, 0o660); err != nil {
		return err
	}
	return http.Serve(ln, handler)
}

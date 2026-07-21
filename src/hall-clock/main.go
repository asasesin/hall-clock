package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// version is the release tag, set at build time with
// -ldflags "-X main.version=v1.2.3". The updater on the Pi compares it against
// the latest GitHub release to decide whether there is anything to install.
var version = "dev"

func main() {
	var addr string
	var publicURL string
	var configPath string
	var webDir string
	var showVersion bool

	flag.StringVar(&addr, "addr", ":8480", "listen address")
	flag.StringVar(&publicURL, "public-url", "", "controller URL for QR codes")
	flag.StringVar(&configPath, "config", defaultConfigPath(), "path to JSON config file")
	flag.StringVar(&webDir, "web-dir", "", "serve web assets live from this directory instead of the embedded copy (dev)")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return
	}

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
	// ReadHeaderTimeout keeps a stalled client from holding a connection open
	// before it even sends a request; IdleTimeout reaps dead keep-alives.
	// WriteTimeout stays zero on purpose: /events streams for hours.
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	path, isUnix := strings.CutPrefix(addr, "unix:")
	if !isUnix {
		server.Addr = addr
		return server.ListenAndServe()
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
	return server.Serve(ln)
}

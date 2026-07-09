package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
)

type server struct {
	mu          sync.Mutex
	configPath  string
	config      Config
	state       State
	talks       []Talk
	startedAt   time.Time
	remainingAt int
	bellSeq     int64
	subscribers map[chan State]struct{}
	// clock is the time source for all scheduling decisions (meeting-type
	// switching, stale-part purging, timer elapsed). Defaults to time.Now;
	// tests override it to pin a specific day so behaviour is deterministic.
	clock func() time.Time
	// webAssets, when set, serves the HTML/CSS/JS live from disk instead of
	// the copy embedded in the binary. Used for local development so edits
	// show up on refresh without a rebuild; nil in production.
	webAssets fs.FS
	// Self-update plumbing: the app writes updateTriggerPath to ask the
	// root-owned updater to run, and reads back updateStatusPath. See update.go.
	updateTriggerPath string
	updateStatusPath  string
	updates           *updateChecker
}

const currentConfigVersion = 1

func newServer(configPath string) (*server, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}

	if len(config.Schedule) == 0 {
		config.Schedule = defaultSchedule()
	}
	normalizeSchedule(config.Schedule)
	if isWeekendSchedule(config.Schedule) {
		config.Schedule = defaultSchedule()
	}
	if strings.TrimSpace(config.DeviceName) == "" {
		config.DeviceName = "Hall Clock"
	}
	config.MeetingType = normalizeMeetingType(config.MeetingType)
	config.MeetingStartTime = normalizeStartTime(config.MeetingStartTime)
	config.MeetingStarts = normalizeMeetingStarts(config.MeetingStarts, config.MeetingStartTime)
	if config.PrestartSeconds == 0 {
		config.PrestartSeconds = 300
	}
	config.PrestartSeconds = clamp(config.PrestartSeconds, 60, 1800)
	if config.MidweekLanguage == "" {
		config.MidweekLanguage = wolLanguage(config.MidweekURL)
	}
	if config.MidweekLanguageSources == nil {
		config.MidweekLanguageSources = map[string]string{}
	}
	if config.MidweekLanguage != "" && config.MidweekURL != "" {
		config.MidweekLanguageSources[config.MidweekLanguage] = config.MidweekURL
	}
	if config.MidweekLanguage != "" && config.MidweekImportedWeek != "" && len(config.Schedule) > 0 {
		if config.MidweekLanguageSchedules == nil {
			config.MidweekLanguageSchedules = map[string]MidweekLanguageSchedule{}
		}
		cached := append([]Talk(nil), config.Schedule...)
		normalizeSchedule(cached)
		config.MidweekLanguageSchedules[config.MidweekLanguage] = MidweekLanguageSchedule{
			ImportedWeek: config.MidweekImportedWeek,
			URL:          config.MidweekURL,
			Schedule:     cached,
		}
	}
	if config.Version < currentConfigVersion {
		config.AutoImportMidweek = true
		config.Version = currentConfigVersion
	}
	if config.ControlToken == "" {
		config.ControlToken, err = newToken()
		if err != nil {
			return nil, err
		}
	}
	if err := saveConfig(configPath, config); err != nil {
		return nil, err
	}

	now := time.Now()
	coActive := circuitOverseerActive(config.CircuitOverseerExpiresAt, now)
	activeMeetingType := meetingTypeForTime(now)
	activeSchedule := scheduleForMeetingType(activeMeetingType, config.Schedule, coActive, config.MidweekLanguage)
	first := activeSchedule[0]
	return &server{
		configPath: configPath,
		config:     config,
		state: State{
			Status:                   StatusIdle,
			DeviceName:               config.DeviceName,
			MeetingType:              activeMeetingType,
			MeetingStartTime:         config.MeetingStartTime,
			MeetingStarts:            config.MeetingStarts,
			PrestartLabel:            "",
			PrestartSeconds:          config.PrestartSeconds,
			CurrentTalkID:            first.ID,
			CurrentTalkTitle:         first.Title,
			DurationSeconds:          first.Duration,
			RemainingSeconds:         first.Duration,
			ClosingSeconds:           first.Closing,
			CircuitOverseer:          coActive,
			CircuitOverseerExpiresAt: circuitOverseerExpiryPtr(config.CircuitOverseerExpiresAt, now),
			MidweekLanguage:          config.MidweekLanguage,
			Schedule:                 activeSchedule,
			Now:                      now,
		},
		talks:             activeSchedule,
		remainingAt:       first.Duration,
		subscribers:       map[chan State]struct{}{},
		clock:             time.Now,
		updateTriggerPath: defaultUpdateTriggerPath,
		updateStatusPath:  defaultUpdateStatusPath,
		updates:           &updateChecker{repo: defaultUpdateRepo},
	}, nil
}

func (s *server) routes(publicURL string) (*http.ServeMux, error) {
	mux := http.NewServeMux()
	static := s.webAssets
	if static == nil {
		sub, err := fs.Sub(webFS, "web")
		if err != nil {
			return nil, err
		}
		static = sub
	}

	assets := http.FileServer(http.FS(static))
	if s.webAssets != nil {
		// Live-from-disk mode: stop the browser caching so edits show up on
		// refresh without a rebuild.
		assets = noCache(assets)
	}
	mux.Handle("GET /assets/", assets)
	// Root serves the phone controller so the printed QR can be a clean
	// http://hallclock.local (no /control). The TV kiosk opens /display
	// explicitly, and /control stays as an alias for existing links/QRs.
	mux.HandleFunc("GET /{$}", s.servePage(static, "control.html"))
	mux.HandleFunc("GET /display", s.servePage(static, "display.html"))
	mux.HandleFunc("GET /pair", s.servePage(static, "pair.html"))
	mux.HandleFunc("GET /control", s.servePage(static, "control.html"))
	mux.HandleFunc("GET /setup", s.servePage(static, "setup.html"))
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/update", s.handleUpdateInfo)
	mux.HandleFunc("POST /api/update", s.protect(s.handleUpdateStart))
	mux.HandleFunc("GET /api/pairing", s.handlePairing(publicURL))
	mux.HandleFunc("POST /api/pairing/enable", s.protect(s.handleEnablePairing))
	mux.HandleFunc("POST /api/control/start", s.protect(s.handleStart))
	mux.HandleFunc("POST /api/control/pause", s.protect(s.handlePause))
	mux.HandleFunc("POST /api/control/reset", s.protect(s.handleReset))
	mux.HandleFunc("POST /api/control/next", s.protect(s.handleNext))
	mux.HandleFunc("POST /api/control/previous", s.protect(s.handlePrevious))
	mux.HandleFunc("POST /api/control/adjust", s.protect(s.handleAdjust))
	mux.HandleFunc("POST /api/control/time", s.protect(s.handleSetTime))
	mux.HandleFunc("POST /api/control/select", s.protect(s.handleSelect))
	mux.HandleFunc("POST /api/control/adhoc-part", s.protect(s.handleAdhocPart))
	mux.HandleFunc("POST /api/control/move-part", s.protect(s.handleMovePart))
	mux.HandleFunc("POST /api/control/bell", s.protect(s.handleBell))
	mux.HandleFunc("POST /api/control/circuit-overseer", s.protect(s.handleCircuitOverseer))
	mux.HandleFunc("POST /api/control/midweek-language", s.protect(s.handleMidweekLanguage))
	mux.HandleFunc("POST /api/config", s.protect(s.handleSaveConfig))
	mux.HandleFunc("POST /api/import/midweek", s.protect(s.handleImportMidweek))
	mux.HandleFunc("POST /api/import/midweek-text", s.protect(s.handleImportMidweekText))
	mux.HandleFunc("POST /api/template/weekend", s.protect(s.handleWeekendTemplate))
	mux.HandleFunc("POST /api/template/midweek", s.protect(s.handleMidweekTemplate))
	mux.HandleFunc("GET /qr.png", s.handleQR(publicURL))

	return mux, nil
}

func (s *server) servePage(static fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.webAssets != nil {
			w.Header().Set("Cache-Control", "no-store")
		}
		http.ServeFileFS(w, r, static, name)
	}
}

// noCache wraps a handler so responses are never cached by the browser. Used
// only in live-from-disk dev mode so edits appear on refresh.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *server) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Wall-Clock-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		s.mu.Lock()
		expected := s.config.ControlToken
		s.mu.Unlock()
		if token == "" || token != expected {
			http.Error(w, "missing or invalid control token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *server) handlePairing(publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		token := s.config.ControlToken
		configuredURL := s.config.AdvertisedBaseURL
		s.mu.Unlock()

		target := advertisedControlURL(publicURL, configuredURL, r)
		writeJSON(w, map[string]string{
			"controlUrl": withToken(target, token),
		})
	}
}

func (s *server) handleEnablePairing(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan State, 8)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	initial := s.snapshotLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
		close(ch)
	}()

	writeEvent(w, initial)
	flusher.Flush()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case state := <-ch:
			writeEvent(w, state)
			flusher.Flush()
		case <-ticker.C:
			state := s.snapshot()
			writeEvent(w, state)
			flusher.Flush()
		}
	}
}

func (s *server) handleQR(publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		token := s.config.ControlToken
		configuredURL := s.config.AdvertisedBaseURL
		s.mu.Unlock()

		target := advertisedControlURL(publicURL, configuredURL, r)
		target = withToken(target, token)

		png, err := qrcode.Encode(target, qrcode.Medium, 512)
		if err != nil {
			http.Error(w, "could not generate QR code", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = bytes.NewReader(png).WriteTo(w)
	}
}

func (s *server) snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *server) snapshotLocked() State {
	s.recalculateLocked(s.clock())
	out := s.state
	out.Schedule = append([]Talk(nil), s.talks...)
	out.MeetingStarts = append([]MeetingStart(nil), s.config.MeetingStarts...)
	out.MidweekLanguage = s.config.MidweekLanguage
	out.PairingActive = true
	out.PairingExpiresAt = nil
	return out
}

func (s *server) broadcast(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- state:
		default:
		}
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeEvent(w http.ResponseWriter, state State) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
}

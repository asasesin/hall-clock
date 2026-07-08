package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web
var webFS embed.FS

type TimerStatus string

const (
	StatusIdle    TimerStatus = "idle"
	StatusRunning TimerStatus = "running"
	StatusPaused  TimerStatus = "paused"
)

const autoImportHour = 3

type Talk struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"durationSeconds"`
	Closing  int    `json:"closingSeconds"`
}

type Config struct {
	DeviceName          string         `json:"deviceName"`
	ControlToken        string         `json:"controlToken"`
	MeetingType         string         `json:"meetingType"`
	MeetingStartTime    string         `json:"meetingStartTime"`
	MeetingStarts       []MeetingStart `json:"meetingStarts"`
	PrestartSeconds     int            `json:"prestartSeconds"`
	MidweekURL          string         `json:"midweekUrl"`
	AutoImportMidweek   bool           `json:"autoImportMidweek"`
	MidweekImportedWeek string         `json:"midweekImportedWeek,omitempty"`
	Schedule            []Talk         `json:"schedule"`
}

type MeetingStart struct {
	ID           int    `json:"id"`
	Day          int    `json:"day"`
	Time         string `json:"time"`
	Congregation string `json:"congregation"`
}

type State struct {
	Status            TimerStatus    `json:"status"`
	DeviceName        string         `json:"deviceName"`
	MeetingType       string         `json:"meetingType"`
	MeetingStartTime  string         `json:"meetingStartTime"`
	MeetingStarts     []MeetingStart `json:"meetingStarts"`
	PrestartLabel     string         `json:"prestartLabel"`
	PrestartSeconds   int            `json:"prestartSeconds"`
	PrestartActive    bool           `json:"prestartActive"`
	PrestartRemaining int            `json:"prestartRemainingSeconds"`
	CurrentTalkID     int            `json:"currentTalkId"`
	CurrentTalkTitle  string         `json:"currentTalkTitle"`
	DurationSeconds   int            `json:"durationSeconds"`
	RemainingSeconds  int            `json:"remainingSeconds"`
	ElapsedSeconds    int            `json:"elapsedSeconds"`
	ClosingSeconds    int            `json:"closingSeconds"`
	OvertimeSeconds   int            `json:"overtimeSeconds"`
	Schedule          []Talk         `json:"schedule"`
	Now               time.Time      `json:"now"`
	Bell              int64          `json:"bell"`
	PairingActive     bool           `json:"pairingActive"`
	PairingExpiresAt  *time.Time     `json:"pairingExpiresAt,omitempty"`
}

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
}

func main() {
	var addr string
	var publicURL string
	var configPath string

	flag.StringVar(&addr, "addr", ":8080", "listen address")
	flag.StringVar(&publicURL, "public-url", "", "controller URL for QR codes")
	flag.StringVar(&configPath, "config", defaultConfigPath(), "path to JSON config file")
	flag.Parse()

	srv, err := newServer(configPath)
	if err != nil {
		log.Fatal(err)
	}
	mux, err := srv.routes(publicURL)
	if err != nil {
		log.Fatal(err)
	}

	go srv.autoImportLoop()

	log.Printf("wall-clock listening on %s", addr)
	log.Printf("display: http://%s/display", displayHost(addr))
	log.Printf("control: http://%s/control", displayHost(addr))
	log.Printf("config: %s", configPath)

	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newServer(configPath string) (*server, error) {
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}

	if len(config.Schedule) == 0 {
		config.Schedule = defaultSchedule()
	}
	normalizeSchedule(config.Schedule)
	if strings.TrimSpace(config.DeviceName) == "" {
		config.DeviceName = "Wall Clock"
	}
	config.MeetingType = normalizeMeetingType(config.MeetingType)
	config.MeetingStartTime = normalizeStartTime(config.MeetingStartTime)
	config.MeetingStarts = normalizeMeetingStarts(config.MeetingStarts, config.MeetingStartTime)
	if config.PrestartSeconds == 0 {
		config.PrestartSeconds = 300
	}
	config.PrestartSeconds = clamp(config.PrestartSeconds, 60, 1800)
	if config.ControlToken == "" {
		config.ControlToken, err = newToken()
		if err != nil {
			return nil, err
		}
	}
	if err := saveConfig(configPath, config); err != nil {
		return nil, err
	}

	first := config.Schedule[0]
	return &server{
		configPath: configPath,
		config:     config,
		state: State{
			Status:           StatusIdle,
			DeviceName:       config.DeviceName,
			MeetingType:      config.MeetingType,
			MeetingStartTime: config.MeetingStartTime,
			MeetingStarts:    config.MeetingStarts,
			PrestartLabel:    "",
			PrestartSeconds:  config.PrestartSeconds,
			CurrentTalkID:    first.ID,
			CurrentTalkTitle: first.Title,
			DurationSeconds:  first.Duration,
			RemainingSeconds: first.Duration,
			ClosingSeconds:   first.Closing,
			Schedule:         config.Schedule,
			Now:              time.Now(),
		},
		talks:       config.Schedule,
		remainingAt: first.Duration,
		subscribers: map[chan State]struct{}{},
	}, nil
}

func (s *server) routes(publicURL string) (*http.ServeMux, error) {
	mux := http.NewServeMux()
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}

	mux.Handle("GET /assets/", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /", redirect("/display"))
	mux.HandleFunc("GET /display", servePage(static, "display.html"))
	mux.HandleFunc("GET /pair", servePage(static, "pair.html"))
	mux.HandleFunc("GET /control", servePage(static, "control.html"))
	mux.HandleFunc("GET /setup", servePage(static, "setup.html"))
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/pairing", s.handlePairing(publicURL))
	mux.HandleFunc("POST /api/pairing/enable", s.protect(s.handleEnablePairing))
	mux.HandleFunc("POST /api/control/start", s.protect(s.handleStart))
	mux.HandleFunc("POST /api/control/pause", s.protect(s.handlePause))
	mux.HandleFunc("POST /api/control/reset", s.protect(s.handleReset))
	mux.HandleFunc("POST /api/control/next", s.protect(s.handleNext))
	mux.HandleFunc("POST /api/control/previous", s.protect(s.handlePrevious))
	mux.HandleFunc("POST /api/control/adjust", s.protect(s.handleAdjust))
	mux.HandleFunc("POST /api/control/select", s.protect(s.handleSelect))
	mux.HandleFunc("POST /api/control/bell", s.protect(s.handleBell))
	mux.HandleFunc("POST /api/config", s.protect(s.handleSaveConfig))
	mux.HandleFunc("POST /api/import/midweek", s.protect(s.handleImportMidweek))
	mux.HandleFunc("POST /api/import/midweek-text", s.protect(s.handleImportMidweekText))
	mux.HandleFunc("POST /api/template/weekend", s.protect(s.handleWeekendTemplate))
	mux.HandleFunc("POST /api/template/midweek", s.protect(s.handleMidweekTemplate))
	mux.HandleFunc("GET /qr.png", s.handleQR(publicURL))

	return mux, nil
}

func redirect(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, path, http.StatusFound)
	}
}

func servePage(static fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, static, name)
	}
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

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.snapshot())
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := Config{
		DeviceName:          s.config.DeviceName,
		MeetingType:         s.config.MeetingType,
		MeetingStartTime:    s.config.MeetingStartTime,
		MeetingStarts:       append([]MeetingStart(nil), s.config.MeetingStarts...),
		PrestartSeconds:     s.config.PrestartSeconds,
		MidweekURL:          s.config.MidweekURL,
		AutoImportMidweek:   s.config.AutoImportMidweek,
		MidweekImportedWeek: s.config.MidweekImportedWeek,
		Schedule:            append([]Talk(nil), s.talks...),
	}
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *server) handlePairing(publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		token := s.config.ControlToken
		s.mu.Unlock()

		target := publicURL
		if target == "" {
			target = requestBaseURL(r) + "/control"
		}
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

func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.state.Status != StatusRunning {
		s.startedAt = time.Now()
		s.state.Status = StatusRunning
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.recalculateLocked(time.Now())
	if s.state.Status == StatusRunning {
		s.remainingAt = s.state.RemainingSeconds
		s.state.Status = StatusPaused
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.state.Status = StatusIdle
	s.remainingAt = s.state.DurationSeconds
	s.state.RemainingSeconds = s.state.DurationSeconds
	s.state.ElapsedSeconds = 0
	s.state.OvertimeSeconds = 0
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleNext(w http.ResponseWriter, r *http.Request) {
	s.changeTalk(w, 1)
}

func (s *server) handlePrevious(w http.ResponseWriter, r *http.Request) {
	s.changeTalk(w, -1)
}

func (s *server) handleAdjust(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeltaSeconds int `json:"deltaSeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.recalculateLocked(time.Now())
	s.state.DurationSeconds = max(60, s.state.DurationSeconds+body.DeltaSeconds)
	s.state.RemainingSeconds = max(-3600, s.state.RemainingSeconds+body.DeltaSeconds)
	s.remainingAt = s.state.RemainingSeconds
	if s.state.Status == StatusRunning {
		s.startedAt = time.Now()
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleSelect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TalkID int `json:"talkId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	ok := s.selectTalkLocked(body.TalkID)
	state := s.snapshotLocked()
	s.mu.Unlock()

	if !ok {
		http.Error(w, "talk not found", http.StatusNotFound)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleBell(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.bellSeq++
	s.state.Bell = s.bellSeq
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var body Config
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(body.Schedule) == 0 {
		http.Error(w, "schedule cannot be empty", http.StatusBadRequest)
		return
	}

	normalizeSchedule(body.Schedule)
	body.DeviceName = strings.TrimSpace(body.DeviceName)
	if body.DeviceName == "" {
		body.DeviceName = "Wall Clock"
	}
	body.MeetingType = normalizeMeetingType(body.MeetingType)
	body.MeetingStartTime = normalizeStartTime(body.MeetingStartTime)
	body.MeetingStarts = normalizeMeetingStarts(body.MeetingStarts, body.MeetingStartTime)
	if body.PrestartSeconds == 0 {
		body.PrestartSeconds = 300
	}
	body.PrestartSeconds = clamp(body.PrestartSeconds, 60, 1800)

	s.mu.Lock()
	s.config.DeviceName = body.DeviceName
	s.config.MeetingType = body.MeetingType
	s.config.MeetingStartTime = body.MeetingStartTime
	s.config.MeetingStarts = body.MeetingStarts
	s.config.PrestartSeconds = body.PrestartSeconds
	s.config.MidweekURL = strings.TrimSpace(body.MidweekURL)
	s.config.AutoImportMidweek = body.AutoImportMidweek
	s.config.Schedule = body.Schedule
	s.state.DeviceName = body.DeviceName
	s.state.MeetingType = body.MeetingType
	s.state.MeetingStartTime = body.MeetingStartTime
	s.state.MeetingStarts = body.MeetingStarts
	s.state.PrestartLabel = ""
	s.state.PrestartSeconds = body.PrestartSeconds
	s.applyScheduleLocked(body.Schedule)
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save config", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)

	now := time.Now()
	if shouldAutoImportNow(now, config.AutoImportMidweek && config.MeetingType == "midweek", config.MidweekImportedWeek) {
		go s.autoImportTick(context.Background(), now)
	}
}

func (s *server) handleImportMidweek(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string `json:"url"`
		Apply bool   `json:"apply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	sourceURL := strings.TrimSpace(body.URL)
	if sourceURL == "" {
		http.Error(w, "midweek URL is required", http.StatusBadRequest)
		return
	}

	schedule, err := importMidweekFromURL(r.Context(), sourceURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !body.Apply {
		writeJSON(w, map[string]any{
			"meetingType": "midweek",
			"sourceUrl":   sourceURL,
			"schedule":    schedule,
		})
		return
	}

	s.mu.Lock()
	s.config.MeetingType = "midweek"
	s.config.MidweekURL = sourceURL
	s.config.MidweekImportedWeek = isoWeekString(time.Now())
	s.config.Schedule = schedule
	s.state.MeetingType = "midweek"
	s.applyScheduleLocked(schedule)
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save imported schedule", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleImportMidweekText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text  string `json:"text"`
		Apply bool   `json:"apply"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	schedule, err := parseMidweekTimings(body.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !body.Apply {
		writeJSON(w, map[string]any{
			"meetingType": "midweek",
			"schedule":    schedule,
		})
		return
	}

	s.mu.Lock()
	s.config.MeetingType = "midweek"
	s.config.MidweekImportedWeek = isoWeekString(time.Now())
	s.config.Schedule = schedule
	s.state.MeetingType = "midweek"
	s.applyScheduleLocked(schedule)
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save imported schedule", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleWeekendTemplate(w http.ResponseWriter, r *http.Request) {
	s.applyTemplate(w, "weekend", weekendSchedule())
}

func (s *server) handleMidweekTemplate(w http.ResponseWriter, r *http.Request) {
	s.applyTemplate(w, "midweek", defaultSchedule())
}

func (s *server) applyTemplate(w http.ResponseWriter, meetingType string, schedule []Talk) {
	normalizeSchedule(schedule)

	s.mu.Lock()
	s.config.MeetingType = meetingType
	if meetingType == "weekend" && !hasWeekendStart(s.config.MeetingStarts) {
		starts := append(s.config.MeetingStarts, MeetingStart{Day: int(time.Sunday), Time: "10:00"})
		s.config.MeetingStarts = normalizeMeetingStarts(starts, s.config.MeetingStartTime)
		s.state.MeetingStarts = s.config.MeetingStarts
	}
	s.config.Schedule = schedule
	s.state.MeetingType = meetingType
	s.applyScheduleLocked(schedule)
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save template", http.StatusInternalServerError)
		return
	}

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
		s.mu.Unlock()

		target := publicURL
		if target == "" {
			target = requestBaseURL(r) + "/control"
		}
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

func (s *server) changeTalk(w http.ResponseWriter, delta int) {
	s.mu.Lock()
	idx := 0
	for i, talk := range s.talks {
		if talk.ID == s.state.CurrentTalkID {
			idx = i
			break
		}
	}
	next := (idx + delta + len(s.talks)) % len(s.talks)
	s.selectTalkLocked(s.talks[next].ID)
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

// applyScheduleLocked swaps in a new schedule without killing an in-progress
// timer: while running or paused, the current talk keeps counting and an
// edited duration shifts the remaining time like a manual adjust would.
func (s *server) applyScheduleLocked(schedule []Talk) {
	s.talks = schedule
	s.state.Schedule = schedule

	if s.state.Status == StatusIdle {
		s.selectTalkLocked(schedule[0].ID)
		return
	}
	for _, talk := range schedule {
		if talk.ID != s.state.CurrentTalkID {
			continue
		}
		s.recalculateLocked(time.Now())
		if delta := talk.Duration - s.state.DurationSeconds; delta != 0 {
			s.state.DurationSeconds = talk.Duration
			s.state.RemainingSeconds += delta
			s.remainingAt = s.state.RemainingSeconds
			if s.state.Status == StatusRunning {
				s.startedAt = time.Now()
			}
		}
		s.state.CurrentTalkTitle = talk.Title
		s.state.ClosingSeconds = talk.Closing
		return
	}
	s.selectTalkLocked(schedule[0].ID)
}

func (s *server) selectTalkLocked(talkID int) bool {
	for _, talk := range s.talks {
		if talk.ID == talkID {
			s.state.Status = StatusIdle
			s.state.CurrentTalkID = talk.ID
			s.state.CurrentTalkTitle = talk.Title
			s.state.DurationSeconds = talk.Duration
			s.state.RemainingSeconds = talk.Duration
			s.state.ElapsedSeconds = 0
			s.state.ClosingSeconds = talk.Closing
			s.state.OvertimeSeconds = 0
			s.remainingAt = talk.Duration
			return true
		}
	}
	return false
}

func (s *server) snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *server) snapshotLocked() State {
	s.recalculateLocked(time.Now())
	out := s.state
	out.Schedule = append([]Talk(nil), s.talks...)
	out.MeetingStarts = append([]MeetingStart(nil), s.config.MeetingStarts...)
	out.PairingActive = true
	out.PairingExpiresAt = nil
	return out
}

func (s *server) recalculateLocked(now time.Time) {
	s.state.Now = now
	s.state.PrestartActive = false
	s.state.PrestartRemaining = 0
	s.state.PrestartLabel = ""
	if s.state.Status != StatusRunning {
		if s.state.Status == StatusIdle {
			if remaining, label, startTime, ok := prestartRemaining(now, s.config.MeetingStarts, s.config.PrestartSeconds); ok {
				s.state.PrestartActive = true
				s.state.PrestartRemaining = remaining
				s.state.PrestartLabel = label
				s.state.MeetingStartTime = startTime
			}
		}
		return
	}

	elapsed := int(now.Sub(s.startedAt).Seconds())
	remaining := s.remainingAt - elapsed
	s.state.RemainingSeconds = remaining
	s.state.ElapsedSeconds = s.state.DurationSeconds - remaining
	if remaining < 0 {
		s.state.OvertimeSeconds = -remaining
	} else {
		s.state.OvertimeSeconds = 0
	}
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

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func saveConfig(path string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func defaultConfigPath() string {
	if path := os.Getenv("WALL_CLOCK_CONFIG"); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "wall-clock", "config.json")
	}
	return "wall-clock.json"
}

func defaultSchedule() []Talk {
	return []Talk{
		{ID: 1, Title: "Opening Comments", Duration: 60, Closing: 30},
		{ID: 2, Title: "Treasures From God's Word", Duration: 600, Closing: 120},
		{ID: 3, Title: "Spiritual Gems", Duration: 600, Closing: 120},
		{ID: 4, Title: "Bible Reading", Duration: 240, Closing: 120},
		{ID: 5, Title: "Apply Yourself to the Field Ministry", Duration: 300, Closing: 120},
		{ID: 6, Title: "Living as Christians", Duration: 900, Closing: 120},
		{ID: 7, Title: "Congregation Bible Study", Duration: 1800, Closing: 120},
		{ID: 8, Title: "Concluding Comments", Duration: 180, Closing: 60},
	}
}

func weekendSchedule() []Talk {
	return []Talk{
		{ID: 1, Title: "Public Talk", Duration: 1800, Closing: 300},
		{ID: 2, Title: "Watchtower Study", Duration: 3600, Closing: 300},
	}
}

func normalizeMeetingType(meetingType string) string {
	switch strings.ToLower(strings.TrimSpace(meetingType)) {
	case "weekend":
		return "weekend"
	default:
		return "midweek"
	}
}

func normalizeStartTime(startTime string) string {
	startTime = strings.TrimSpace(startTime)
	if startTime == "" {
		return "19:00"
	}
	parsed, err := parseClockTime(startTime)
	if err != nil {
		return "19:00"
	}
	return fmt.Sprintf("%02d:%02d", parsed.hour, parsed.minute)
}

func prestartRemaining(now time.Time, starts []MeetingStart, prestartSeconds int) (int, string, string, bool) {
	today := int(now.Weekday())
	bestRemaining := 0
	bestLabel := ""
	bestTime := ""
	found := false

	for _, slot := range starts {
		if slot.Day != today {
			continue
		}
		hourMinute, err := parseClockTime(slot.Time)
		if err != nil {
			continue
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), hourMinute.hour, hourMinute.minute, 0, 0, now.Location())
		windowStart := start.Add(-time.Duration(prestartSeconds) * time.Second)
		if now.Before(windowStart) || !now.Before(start) {
			continue
		}
		remaining := int(start.Sub(now).Seconds())
		if !found || remaining < bestRemaining {
			bestRemaining = remaining
			bestLabel = strings.TrimSpace(slot.Congregation)
			bestTime = slot.Time
			found = true
		}
	}

	return bestRemaining, bestLabel, bestTime, found
}

type clockTime struct {
	hour   int
	minute int
}

func parseClockTime(value string) (clockTime, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return clockTime{}, errors.New("invalid time")
	}

	hour, err := parsePositiveInt(parts[0])
	if err != nil {
		return clockTime{}, err
	}
	minute, err := parsePositiveInt(parts[1])
	if err != nil {
		return clockTime{}, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return clockTime{}, errors.New("invalid time")
	}
	return clockTime{hour: hour, minute: minute}, nil
}

func normalizeMeetingStarts(starts []MeetingStart, fallbackStartTime string) []MeetingStart {
	if len(starts) == 0 {
		return defaultMeetingStarts(fallbackStartTime)
	}

	normalized := make([]MeetingStart, 0, len(starts))
	for _, start := range starts {
		if start.Day < int(time.Sunday) || start.Day > int(time.Saturday) {
			continue
		}
		start.Time = normalizeStartTime(start.Time)
		start.Congregation = strings.TrimSpace(start.Congregation)
		normalized = append(normalized, start)
	}
	if len(normalized) == 0 {
		return defaultMeetingStarts(fallbackStartTime)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Day != normalized[j].Day {
			return normalized[i].Day < normalized[j].Day
		}
		return normalized[i].Time < normalized[j].Time
	})
	for i := range normalized {
		normalized[i].ID = i + 1
	}
	return normalized
}

func defaultMeetingStarts(startTime string) []MeetingStart {
	startTime = normalizeStartTime(startTime)
	starts := make([]MeetingStart, 0, 6)
	starts = append(starts, MeetingStart{ID: 1, Day: int(time.Sunday), Time: "10:00"})
	for day := int(time.Monday); day <= int(time.Friday); day++ {
		starts = append(starts, MeetingStart{
			ID:           len(starts) + 1,
			Day:          day,
			Time:         startTime,
			Congregation: "",
		})
	}
	return starts
}

func hasWeekendStart(starts []MeetingStart) bool {
	for _, start := range starts {
		if start.Day == int(time.Sunday) || start.Day == int(time.Saturday) {
			return true
		}
	}
	return false
}

func normalizeSchedule(schedule []Talk) {
	for i := range schedule {
		schedule[i].ID = i + 1
		schedule[i].Title = strings.TrimSpace(schedule[i].Title)
		if schedule[i].Title == "" {
			schedule[i].Title = fmt.Sprintf("Part %d", i+1)
		}
		schedule[i].Duration = clamp(schedule[i].Duration, 60, 7200)
		schedule[i].Closing = clamp(schedule[i].Closing, 0, schedule[i].Duration)
	}
}

func importMidweekFromURL(ctx context.Context, sourceURL string) ([]Talk, error) {
	body, err := fetchWOLPage(ctx, sourceURL)
	if err != nil {
		return nil, err
	}
	return parseMidweekTimings(body)
}

func fetchWOLPage(ctx context.Context, sourceURL string) (string, error) {
	if !strings.HasPrefix(sourceURL, "https://wol.jw.org/") && !strings.HasPrefix(sourceURL, "http://wol.jw.org/") {
		return "", errors.New("only wol.jw.org URLs are supported")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "wall-clock-local-appliance/0.1")

	client := http.Client{Timeout: 12 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not fetch midweek page: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("midweek page returned HTTP %d", res.StatusCode)
	}

	return readLimitedString(res.Body, 2<<20)
}

var wolDocURLPattern = regexp.MustCompile(`^https?://wol\.jw\.org/([a-z-]+)/wol/[a-z]+/(r\d+)/(lp-[a-z0-9-]+)/`)

// weeklyMeetingsURL builds the date-addressable WOL page for the current ISO
// week, keeping the language/library segments of a previously used URL so
// non-English configurations stay in their own language.
func weeklyMeetingsURL(exampleURL string, now time.Time) string {
	lang, rsconf, lib := "en", "r1", "lp-e"
	if m := wolDocURLPattern.FindStringSubmatch(exampleURL); m != nil {
		lang, rsconf, lib = m[1], m[2], m[3]
	}
	year, week := now.ISOWeek()
	return fmt.Sprintf("https://wol.jw.org/%s/wol/meetings/%s/%s/%d/%d", lang, rsconf, lib, year, week)
}

// findWorkbookDocURL extracts the midweek workbook document link from a weekly
// meetings page. Workbook docids are 9 digits, which distinguishes them from
// the Watchtower study article also linked on that page.
func findWorkbookDocURL(page string) (string, bool) {
	m := regexp.MustCompile(`href="(/[a-z-]+/wol/d/r\d+/lp-[a-z0-9-]+/\d{9})"`).FindStringSubmatch(page)
	if m == nil {
		return "", false
	}
	return "https://wol.jw.org" + m[1], true
}

func isoWeekString(now time.Time) string {
	year, week := now.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

func (s *server) autoImportLoop() {
	for {
		now := time.Now()
		s.mu.Lock()
		enabled := s.config.AutoImportMidweek && s.config.MeetingType == "midweek"
		importedWeek := s.config.MidweekImportedWeek
		s.mu.Unlock()

		if shouldAutoImportNow(now, enabled, importedWeek) {
			s.autoImportTick(context.Background(), now)
			time.Sleep(time.Hour)
			continue
		}

		time.Sleep(time.Until(nextAutoImportAt(now)))
	}
}

// autoImportTick pulls the current week's midweek program. The caller controls
// the Monday 3:00 AM schedule; this method still guards against duplicate
// imports and disabled auto-import settings.
func (s *server) autoImportTick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	enabled := s.config.AutoImportMidweek && s.config.MeetingType == "midweek"
	exampleURL := s.config.MidweekURL
	importedWeek := s.config.MidweekImportedWeek
	s.mu.Unlock()

	currentWeek := isoWeekString(now)
	if !enabled || importedWeek == currentWeek {
		return
	}

	page, err := fetchWOLPage(ctx, weeklyMeetingsURL(exampleURL, now))
	if err != nil {
		log.Printf("auto-import: %v", err)
		return
	}
	docURL, ok := findWorkbookDocURL(page)
	if !ok {
		log.Printf("auto-import: no workbook link on weekly meetings page")
		return
	}
	schedule, err := importMidweekFromURL(ctx, docURL)
	if err != nil {
		log.Printf("auto-import: %v", err)
		return
	}

	s.mu.Lock()
	s.config.MidweekURL = docURL
	s.config.MidweekImportedWeek = currentWeek
	s.config.Schedule = schedule
	s.applyScheduleLocked(schedule)
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		log.Printf("auto-import: could not save config: %v", err)
	}
	s.broadcast(state)
	log.Printf("auto-import: applied midweek schedule for %s from %s", currentWeek, docURL)
}

func shouldAutoImportNow(now time.Time, enabled bool, importedWeek string) bool {
	if !enabled || importedWeek == isoWeekString(now) {
		return false
	}
	return !now.Before(currentWeekAutoImportAt(now))
}

func currentWeekAutoImportAt(now time.Time) time.Time {
	daysSinceMonday := (int(now.Weekday()) - int(time.Monday) + 7) % 7
	year, month, day := now.Date()
	return time.Date(year, month, day-daysSinceMonday, autoImportHour, 0, 0, 0, now.Location())
}

func nextAutoImportAt(now time.Time) time.Time {
	current := currentWeekAutoImportAt(now)
	if now.Before(current) {
		return current
	}
	return current.AddDate(0, 0, 7)
}

func readLimitedString(reader io.Reader, maxBytes int64) (string, error) {
	var buf bytes.Buffer
	limited := io.LimitReader(reader, maxBytes+1)
	if _, err := buf.ReadFrom(limited); err != nil {
		return "", err
	}
	if int64(buf.Len()) > maxBytes {
		return "", errors.New("midweek page is too large")
	}
	return buf.String(), nil
}

func parseMidweekTimings(input string) ([]Talk, error) {
	text := htmlToText(input)
	text = strings.ReplaceAll(text, "\u00a0", " ")

	linePattern := regexp.MustCompile(`(?i)([^()\n]{2,140}?)\s*\((\d{1,3})\s*min\.?\)`)
	matches := linePattern.FindAllStringSubmatch(text, -1)

	var talks []Talk
	seen := map[string]struct{}{}
	for _, match := range matches {
		title := cleanTimingTitle(match[1])
		if title == "" {
			continue
		}
		minutes, err := parsePositiveInt(match[2])
		if err != nil || minutes <= 0 || minutes > 120 {
			continue
		}
		key := strings.ToLower(fmt.Sprintf("%s:%d", title, minutes))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		talks = append(talks, Talk{
			ID:       len(talks) + 1,
			Title:    title,
			Duration: minutes * 60,
			Closing:  min(120, minutes*30),
		})
	}

	if len(talks) == 0 {
		return nil, errors.New("no timing slots found")
	}
	normalizeSchedule(talks)
	return talks, nil
}

func htmlToText(input string) string {
	replacements := []struct {
		old string
		new string
	}{
		{"</p>", "\n"},
		{"</div>", "\n"},
		{"</li>", "\n"},
		{"<br>", "\n"},
		{"<br/>", "\n"},
		{"<br />", "\n"},
	}
	for _, replacement := range replacements {
		input = strings.ReplaceAll(input, replacement.old, replacement.new)
	}

	tagPattern := regexp.MustCompile(`<[^>]+>`)
	input = tagPattern.ReplaceAllString(input, " ")
	entityPattern := regexp.MustCompile(`&[^;\s]+;`)
	input = entityPattern.ReplaceAllStringFunc(input, decodeHTMLEntity)
	spacePattern := regexp.MustCompile(`[ \t]+`)
	input = spacePattern.ReplaceAllString(input, " ")
	linePattern := regexp.MustCompile(`\n\s+`)
	return linePattern.ReplaceAllString(input, "\n")
}

func decodeHTMLEntity(entity string) string {
	switch entity {
	case "&amp;":
		return "&"
	case "&quot;":
		return `"`
	case "&#39;", "&apos;":
		return "'"
	case "&nbsp;":
		return " "
	case "&lt;":
		return "<"
	case "&gt;":
		return ">"
	default:
		return " "
	}
}

func cleanTimingTitle(title string) string {
	title = strings.TrimSpace(title)
	if strings.Contains(title, "|") {
		parts := strings.Split(title, "|")
		title = parts[len(parts)-1]
	}
	title = regexp.MustCompile(`^[\s\d.:-]+`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")
	title = strings.Trim(title, " -:\t\r\n")
	if len(title) < 2 {
		return ""
	}
	return title
}

func parsePositiveInt(value string) (int, error) {
	var parsed int
	_, err := fmt.Sscanf(value, "%d", &parsed)
	return parsed, err
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = displayHost(":8080")
	} else {
		host = networkReachableHost(host)
	}
	return scheme + "://" + host
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

func clamp(value, minValue, maxValue int) int {
	return min(max(value, minValue), maxValue)
}

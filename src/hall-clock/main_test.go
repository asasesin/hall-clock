package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProtectedControlRequiresToken(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/control/start", nil)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized response, got %d", res.Code)
	}
}

func TestProtectedControlAcceptsPairingToken(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/control/start", nil)
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK response, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning {
		t.Fatalf("expected running status, got %q", state.Status)
	}
}

func TestPairingEndpointAlwaysReturnsTokenizedControlURL(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.Host = "hallclock.local:8080"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing response, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "http://hallclock.local:8080?token=") {
		t.Fatalf("expected tokenized root control URL, got %s", res.Body.String())
	}
}

func TestPairingEndpointUsesConfiguredPublicURL(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("http://hallclock.local:8080/control")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.Host = "192.168.1.50:8080"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing response, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "http://hallclock.local:8080/control?token=") {
		t.Fatalf("expected configured public URL, got %s", res.Body.String())
	}
}

func TestRequestBaseURLTrustsForwardedFromLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.RemoteAddr = "127.0.0.1:54321" // the co-located reverse proxy
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-Forwarded-Host", "hallclock.local")
	req.Header.Set("X-Forwarded-Proto", "http")

	got := requestBaseURL(req)
	if got != "http://hallclock.local" {
		t.Fatalf("expected portless proxied origin, got %q", got)
	}
}

func TestRequestBaseURLIgnoresForwardedFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.RemoteAddr = "192.168.1.77:40000" // a phone on the LAN, not the proxy
	req.Host = "192.168.1.50:8080"
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "https")

	got := requestBaseURL(req)
	if strings.Contains(got, "evil.example") {
		t.Fatalf("must not trust forwarded host from an untrusted peer, got %q", got)
	}
	if got != "http://192.168.1.50:8080" {
		t.Fatalf("expected fallback to request host, got %q", got)
	}
}

func TestPortlessLocalhostIgnoresStaleWallclockOverride(t *testing.T) {
	// Behind a proxy on a standard port the Host has no ":port" (e.g. Caddy on
	// 443 gives Host "localhost"). A stale hallclock.local override must still
	// be ignored so pairing resolves to the reachable request origin.
	cfg := "http://hallclock.local:8080/control"
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		r := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
		r.Host = host
		if shouldUseConfiguredAdvertisedURL(cfg, r) {
			t.Fatalf("host %q: expected stale hallclock.local override to be ignored", host)
		}
	}
}

func TestPairingEndpointUsesSavedAdvertisedURLWhenCLIFlagUnset(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv.config.AdvertisedBaseURL = "http://hallclock.local/control"

	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.Host = "192.168.1.50:8080"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing response, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "http://hallclock.local/control?token=") {
		t.Fatalf("expected saved advertised URL, got %s", res.Body.String())
	}
}

func TestPairingEndpointIgnoresWallclockLocalWhenOpenedFromLocalhost(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv.config.AdvertisedBaseURL = "http://hallclock.local:8080/control"

	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.Host = "localhost:8080"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing response, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "http://") || !strings.Contains(res.Body.String(), ":8080?token=") {
		t.Fatalf("expected fallback LAN root URL, got %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "hallclock.local") {
		t.Fatalf("expected localhost pairing to ignore saved hallclock.local URL, got %s", res.Body.String())
	}
}

func TestSaveConfigKeepsRunningTimer(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	do := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, res.Code, res.Body.String())
		}
		return res
	}

	do("/api/control/select", `{"talkId":2}`)
	do("/api/control/start", "")

	config := Config{Schedule: []Talk{
		{Title: "Opening", Duration: 60, Closing: 30},
		{Title: "Renamed Talk", Duration: 900, Closing: 90},
	}}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	res := do("/api/config", string(body))

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRunning {
		t.Fatalf("expected timer to keep running, got %q", state.Status)
	}
	if state.CurrentTalkID != 2 || state.CurrentTalkTitle != "Renamed Talk" {
		t.Fatalf("expected current talk to be preserved, got %d %q", state.CurrentTalkID, state.CurrentTalkTitle)
	}
	if state.DurationSeconds != 900 || state.ClosingSeconds != 90 {
		t.Fatalf("expected edited timing to apply, got duration=%d closing=%d", state.DurationSeconds, state.ClosingSeconds)
	}
	// Talk 2 started with 600s; the edit adds 300s, so remaining should be ~900.
	if state.RemainingSeconds < 895 || state.RemainingSeconds > 900 {
		t.Fatalf("expected remaining time to shift with the new duration, got %d", state.RemainingSeconds)
	}
}

func TestSaveConfigResetsWhenCurrentTalkRemoved(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	do := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, res.Code, res.Body.String())
		}
		return res
	}

	do("/api/control/select", `{"talkId":3}`)
	do("/api/control/start", "")

	config := Config{Schedule: []Talk{
		{Title: "Only Part", Duration: 300, Closing: 60},
	}}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	res := do("/api/config", string(body))

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusIdle {
		t.Fatalf("expected idle status after current talk removed, got %q", state.Status)
	}
	if state.CurrentTalkID != 1 || state.CurrentTalkTitle != "Only Part" {
		t.Fatalf("expected reset to first talk, got %d %q", state.CurrentTalkID, state.CurrentTalkTitle)
	}
}

func TestSetTimeUpdatesIdleCurrentTimer(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/control/time", strings.NewReader(`{"seconds":240}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK response, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusIdle {
		t.Fatalf("expected timer to stay idle, got %q", state.Status)
	}
	if state.DurationSeconds != 240 || state.RemainingSeconds != 240 {
		t.Fatalf("expected edited time to be 240s, got duration=%d remaining=%d", state.DurationSeconds, state.RemainingSeconds)
	}
	if srv.config.Schedule[0].Duration == 240 {
		t.Fatal("did not expect one-off time edit to rewrite saved schedule")
	}
}

func TestAdhocPartAddsTemporaryPartWithoutSavingConfig(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	savedScheduleLength := len(srv.config.Schedule)
	runtimeScheduleLength := len(srv.talks)

	req := httptest.NewRequest(http.MethodPost, "/api/control/adhoc-part", strings.NewReader(`{"title":"Local announcement","seconds":420}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK response, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Schedule) != runtimeScheduleLength+1 {
		t.Fatalf("expected one temporary part, got %d parts", len(state.Schedule))
	}
	if state.CurrentTalkTitle != "Local announcement" || state.DurationSeconds != 420 {
		t.Fatalf("expected idle timer to select temporary part, got %q for %ds", state.CurrentTalkTitle, state.DurationSeconds)
	}
	if len(srv.config.Schedule) != savedScheduleLength {
		t.Fatalf("expected saved schedule to stay at %d parts, got %d", savedScheduleLength, len(srv.config.Schedule))
	}
}

func TestAdhocPartDoesNotInterruptRunningTimer(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	savedScheduleLength := len(srv.config.Schedule)
	runtimeScheduleLength := len(srv.talks)

	do := func(path, body string) State {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, res.Code, res.Body.String())
		}
		var state State
		if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	running := do("/api/control/start", "")
	state := do("/api/control/adhoc-part", `{"title":"Elder update","seconds":300}`)

	if state.Status != StatusRunning {
		t.Fatalf("expected timer to keep running, got %q", state.Status)
	}
	if state.CurrentTalkID != running.CurrentTalkID || state.CurrentTalkTitle != running.CurrentTalkTitle {
		t.Fatalf("expected current part to stay %d %q, got %d %q", running.CurrentTalkID, running.CurrentTalkTitle, state.CurrentTalkID, state.CurrentTalkTitle)
	}
	if len(state.Schedule) != runtimeScheduleLength+1 {
		t.Fatalf("expected temporary schedule length %d, got %d", runtimeScheduleLength+1, len(state.Schedule))
	}
	if len(state.Schedule) < 2 || state.Schedule[1].Title != "Elder update" {
		t.Fatalf("expected temporary part to be inserted after current part, got %+v", state.Schedule)
	}
	if len(srv.config.Schedule) != savedScheduleLength {
		t.Fatalf("expected saved schedule to stay at %d parts, got %d", savedScheduleLength, len(srv.config.Schedule))
	}
}

func TestSaveConfigPreservesTemporaryParts(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	pinMidweek(t, srv)

	do := func(path, body string) State {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, res.Code, res.Body.String())
		}
		var state State
		if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	added := do("/api/control/adhoc-part", `{"title":"Temporary note","seconds":300}`)
	if len(added.Schedule) < 2 || !added.Schedule[1].Temporary {
		t.Fatalf("expected temporary part in live schedule, got %+v", added.Schedule)
	}

	config := Config{
		DeviceName:       "Hall Clock",
		MeetingType:      "midweek",
		MeetingStartTime: "19:00",
		MeetingStarts:    defaultMeetingStarts("19:00"),
		PrestartSeconds:  300,
		Schedule: []Talk{
			{Title: "Opening Comments", Duration: 60, Closing: 30},
			{Title: "Treasures From God's Word", Duration: 600, Closing: 120},
		},
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	res := do("/api/config", string(body))

	if len(res.Schedule) != 3 {
		t.Fatalf("expected temporary part to survive config save, got %+v", res.Schedule)
	}
	if !res.Schedule[1].Temporary {
		t.Fatalf("expected temporary part to stay in its slot after config save, got %+v", res.Schedule)
	}
}

func TestMoveRejectsSavedScheduleParts(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/control/move-part", strings.NewReader(`{"talkId":1,"delta":1}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusConflict {
		t.Fatalf("expected conflict when moving saved schedule part, got %d: %s", res.Code, res.Body.String())
	}
}

func TestMoveTemporaryPartReordersRuntimeSchedule(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	do := func(path, body string) State {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, res.Code, res.Body.String())
		}
		var state State
		if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	added := do("/api/control/adhoc-part", `{"title":"Temporary note","seconds":300}`)
	tempID := added.CurrentTalkID

	movedUp := do("/api/control/move-part", fmt.Sprintf(`{"talkId":%d,"delta":-1}`, tempID))
	if movedUp.Schedule[0].ID != tempID {
		t.Fatalf("expected temp part to move to the front, got %+v", movedUp.Schedule)
	}
	if !movedUp.Schedule[0].Temporary {
		t.Fatal("expected moved part to remain marked temporary")
	}

	movedDown := do("/api/control/move-part", fmt.Sprintf(`{"talkId":%d,"delta":1}`, tempID))
	if len(movedDown.Schedule) < 2 || movedDown.Schedule[1].ID != tempID {
		t.Fatalf("expected temp part to move back after the first part, got %+v", movedDown.Schedule)
	}
}

func TestNormalizeSchedule(t *testing.T) {
	schedule := []Talk{
		{Title: "  ", Duration: 10, Closing: 500},
		{Title: "Talk", Duration: 9000, Closing: -20},
	}

	normalizeSchedule(schedule)

	if schedule[0].ID != 1 || schedule[0].Title != "Part 1" {
		t.Fatalf("unexpected first talk: %+v", schedule[0])
	}
	if schedule[0].Duration != 60 || schedule[0].Closing != 60 {
		t.Fatalf("unexpected first talk timing: %+v", schedule[0])
	}
	if schedule[1].ID != 2 || schedule[1].Duration != 7200 || schedule[1].Closing != 0 {
		t.Fatalf("unexpected second talk: %+v", schedule[1])
	}
}

func TestAutomaticScheduleSwitchesByWeekendDay(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	srv.syncActiveScheduleLocked(time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)) // Sunday
	if srv.state.MeetingType != "weekend" {
		t.Fatalf("expected weekend meeting type, got %q", srv.state.MeetingType)
	}
	if len(srv.talks) != 2 {
		t.Fatalf("expected 2 weekend parts, got %d", len(srv.talks))
	}
	if srv.talks[0].Duration != 1800 || srv.talks[1].Duration != 3600 {
		t.Fatalf("unexpected weekend schedule: %+v", srv.talks)
	}

	srv.syncActiveScheduleLocked(time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)) // Monday
	if srv.state.MeetingType != "midweek" {
		t.Fatalf("expected midweek meeting type, got %q", srv.state.MeetingType)
	}
	if len(srv.talks) != len(defaultSchedule()) {
		t.Fatalf("expected midweek schedule, got %+v", srv.talks)
	}
}

func TestDefaultMidweekTemplateUsesCorrectOpeningAndClosing(t *testing.T) {
	schedule := defaultSchedule()
	if schedule[0].Title != "Opening Comments" || schedule[0].Duration != 60 {
		t.Fatalf("unexpected opening part: %+v", schedule[0])
	}

	last := schedule[len(schedule)-1]
	if last.Title != "Concluding Comments" || last.Duration != 180 {
		t.Fatalf("unexpected concluding part: %+v", last)
	}
}

func TestPrestartRemaining(t *testing.T) {
	now := time.Date(2026, 7, 6, 18, 56, 30, 0, time.Local)
	remaining, label, startTime, ok := prestartRemaining(now, []MeetingStart{
		{Day: int(time.Monday), Time: "19:00", Congregation: "Main"},
	}, 300)
	if !ok {
		t.Fatal("expected prestart countdown to be active")
	}
	if remaining != 210 {
		t.Fatalf("expected 210 seconds remaining, got %d", remaining)
	}
	if label != "Main" || startTime != "19:00" {
		t.Fatalf("unexpected slot metadata: %q %q", label, startTime)
	}
}

func TestPrestartRemainingOutsideWindow(t *testing.T) {
	starts := []MeetingStart{{Day: int(time.Monday), Time: "19:00"}}
	beforeWindow := time.Date(2026, 7, 6, 18, 54, 59, 0, time.Local)
	if _, _, _, ok := prestartRemaining(beforeWindow, starts, 300); ok {
		t.Fatal("did not expect countdown before prestart window")
	}

	atStart := time.Date(2026, 7, 6, 19, 0, 0, 0, time.Local)
	if _, _, _, ok := prestartRemaining(atStart, starts, 300); ok {
		t.Fatal("did not expect countdown once meeting start time is reached")
	}
}

func TestPrestartRemainingChoosesNextTodaySlot(t *testing.T) {
	now := time.Date(2026, 7, 6, 19, 27, 0, 0, time.Local)
	remaining, label, startTime, ok := prestartRemaining(now, []MeetingStart{
		{Day: int(time.Monday), Time: "19:00", Congregation: "Earlier"},
		{Day: int(time.Monday), Time: "19:30", Congregation: "Second Congregation"},
		{Day: int(time.Tuesday), Time: "19:30", Congregation: "Wrong Day"},
	}, 300)
	if !ok {
		t.Fatal("expected second Monday slot to be active")
	}
	if remaining != 180 || label != "Second Congregation" || startTime != "19:30" {
		t.Fatalf("unexpected countdown slot: remaining=%d label=%q time=%q", remaining, label, startTime)
	}
}

func TestDefaultMeetingStartsCoverWeekdaysAndSunday(t *testing.T) {
	starts := defaultMeetingStarts("19:30")
	if len(starts) != 6 {
		t.Fatalf("expected 6 default starts, got %d", len(starts))
	}
	if starts[0].Day != int(time.Sunday) || starts[0].Time != "10:00" {
		t.Fatalf("expected Sunday 10:00 first, got %+v", starts[0])
	}
	if starts[1].Day != int(time.Monday) || starts[1].Time != "19:30" {
		t.Fatalf("unexpected first weekday start: %+v", starts[1])
	}
	if starts[5].Day != int(time.Friday) {
		t.Fatalf("unexpected last start: %+v", starts[5])
	}
}

func TestNormalizeMeetingStartsKeepsMultipleTimesPerDaySorted(t *testing.T) {
	starts := normalizeMeetingStarts([]MeetingStart{
		{Day: int(time.Monday), Time: "19:30", Congregation: "Evening"},
		{Day: int(time.Monday), Time: "9:30", Congregation: "Morning"},
		{Day: int(time.Sunday), Time: "10:00"},
	}, "19:00")

	if len(starts) != 3 {
		t.Fatalf("expected all 3 starts to survive, got %d: %+v", len(starts), starts)
	}
	if starts[0].Day != int(time.Sunday) {
		t.Fatalf("expected Sunday first after sorting, got %+v", starts[0])
	}
	if starts[1].Time != "09:30" || starts[1].Congregation != "Morning" {
		t.Fatalf("expected padded morning slot second, got %+v", starts[1])
	}
	if starts[2].Time != "19:30" || starts[2].ID != 3 {
		t.Fatalf("expected evening slot last with ID 3, got %+v", starts[2])
	}
}

func TestWeekendTemplateAddsWeekendStartWhenMissing(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv.config.MeetingStarts = []MeetingStart{
		{ID: 1, Day: int(time.Monday), Time: "19:00"},
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/template/weekend", nil)
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK response, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !hasWeekendStart(state.MeetingStarts) {
		t.Fatalf("expected a weekend start to be added, got %+v", state.MeetingStarts)
	}
	if len(state.MeetingStarts) != 2 {
		t.Fatalf("expected weekday start to be kept alongside, got %+v", state.MeetingStarts)
	}
}

func TestNormalizeStartTime(t *testing.T) {
	if got := normalizeStartTime("09:30"); got != "09:30" {
		t.Fatalf("expected valid time to be preserved, got %q", got)
	}
	if got := normalizeStartTime("bad"); got != "19:00" {
		t.Fatalf("expected invalid time to fall back, got %q", got)
	}
}

func TestWeeklyMeetingsURL(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	got := weeklyMeetingsURL("https://wol.jw.org/en/wol/d/r1/lp-e/202026241", now)
	if got != "https://wol.jw.org/en/wol/meetings/r1/lp-e/2026/28" {
		t.Fatalf("unexpected weekly URL: %s", got)
	}

	got = weeklyMeetingsURL("", now)
	if got != "https://wol.jw.org/en/wol/meetings/r1/lp-e/2026/28" {
		t.Fatalf("expected English defaults without an example URL, got %s", got)
	}

	got = weeklyMeetingsURL("https://wol.jw.org/es/wol/d/r4/lp-s/202026241", now)
	if got != "https://wol.jw.org/es/wol/meetings/r4/lp-s/2026/28" {
		t.Fatalf("expected language segments to carry over, got %s", got)
	}
}

func TestFindWorkbookDocURL(t *testing.T) {
	page := `
		<a href="/en/wol/d/r1/lp-e/2026400">Watchtower study article</a>
		<a href="/en/wol/d/r1/lp-e/202026241">Midweek workbook</a>
	`
	got, ok := findWorkbookDocURL(page)
	if !ok {
		t.Fatal("expected to find workbook link")
	}
	if got != "https://wol.jw.org/en/wol/d/r1/lp-e/202026241" {
		t.Fatalf("expected 9-digit workbook docid, got %s", got)
	}

	if _, ok := findWorkbookDocURL(`<a href="/en/wol/d/r1/lp-e/2026400">Watchtower</a>`); ok {
		t.Fatal("did not expect a match without a workbook link")
	}
}

func TestAutoImportTickSkipsWhenDisabledOrCurrent(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	before := srv.snapshot().Schedule

	// Disabled: must not touch anything (would fail on network in CI otherwise).
	srv.autoImportTick(t.Context(), now)

	// Enabled but already imported this week: must return before fetching.
	srv.mu.Lock()
	srv.config.AutoImportMidweek = true
	srv.config.MidweekImportedWeek = isoWeekString(now)
	srv.mu.Unlock()
	srv.autoImportTick(t.Context(), now)

	after := srv.snapshot().Schedule
	if len(after) != len(before) {
		t.Fatalf("expected schedule to be untouched, got %d parts", len(after))
	}
}

func TestAutoImportScheduleRunsMondayAtThree(t *testing.T) {
	loc := time.FixedZone("test", -5*60*60)
	week := ""

	before := time.Date(2026, 7, 6, 2, 59, 0, 0, loc)
	if shouldAutoImportNow(before, true, week) {
		t.Fatal("did not expect import before Monday 3:00 AM")
	}
	if got := nextAutoImportAt(before); !got.Equal(time.Date(2026, 7, 6, 3, 0, 0, 0, loc)) {
		t.Fatalf("unexpected next import time before 3 AM: %s", got)
	}

	at := time.Date(2026, 7, 6, 3, 0, 0, 0, loc)
	if !shouldAutoImportNow(at, true, week) {
		t.Fatal("expected import at Monday 3:00 AM")
	}
	if got := nextAutoImportAt(at); !got.Equal(time.Date(2026, 7, 13, 3, 0, 0, 0, loc)) {
		t.Fatalf("unexpected next import time after 3 AM: %s", got)
	}

	after := time.Date(2026, 7, 7, 9, 30, 0, 0, loc)
	if !shouldAutoImportNow(after, true, week) {
		t.Fatal("expected missed weekly import to remain due after Monday 3:00 AM")
	}
	if shouldAutoImportNow(after, true, isoWeekString(after)) {
		t.Fatal("did not expect import after current week was already imported")
	}
}

func TestParseMidweekTimings(t *testing.T) {
	input := `
		<h2>July 13-19</h2>
		<p>Song 34 and Prayer | Opening Comments (1 min.)</p>
		<p>It Matters Whom We Trust (10 min.)</p>
		<p>Spiritual Gems (10 min.)</p>
		<p>Bible Reading (4 min.)</p>
		<p>Initial Call (3 min.)</p>
		<p>It Matters Whom We Trust (10 min.)</p>
	`

	schedule, err := parseMidweekTimings(input)
	if err != nil {
		t.Fatal(err)
	}

	if len(schedule) != 5 {
		t.Fatalf("expected 5 unique timing slots, got %d: %+v", len(schedule), schedule)
	}
	if schedule[0].Title != "Opening Comments" || schedule[0].Duration != 60 {
		t.Fatalf("unexpected first slot: %+v", schedule[0])
	}
	if schedule[3].Title != "Bible Reading" || schedule[3].Duration != 240 {
		t.Fatalf("unexpected bible reading slot: %+v", schedule[3])
	}
}

func TestParseMidweekTimingsRejectsEmptyInput(t *testing.T) {
	if _, err := parseMidweekTimings("No timing data here"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestImportMidweekTextEndpointUsesBackendParser(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/import/midweek-text",
		strings.NewReader(`{"text":"Song 1 and Prayer | Opening Comments (1 min.)\nBible Reading (4 min.)"}`),
	)
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK response, got %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "Song 1") {
		t.Fatalf("expected cleaned opening title, got %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Opening Comments") {
		t.Fatalf("expected opening comments in response, got %s", res.Body.String())
	}
}

func TestReadLimitedStringRejectsOversizedImport(t *testing.T) {
	_, err := readLimitedString(strings.NewReader("123456"), 5)
	if err == nil {
		t.Fatal("expected oversized import error")
	}
}

func TestParseMidweekTimingsFromDownloadedFixture(t *testing.T) {
	path := os.Getenv("WALL_CLOCK_WOL_FIXTURE")
	if path == "" {
		t.Skip("set WALL_CLOCK_WOL_FIXTURE to validate against a downloaded WOL page")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := parseMidweekTimings(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) < 8 {
		t.Fatalf("expected at least 8 timing slots, got %d: %+v", len(schedule), schedule)
	}
	t.Logf("parsed schedule: %+v", schedule)
}

// pinMidweek freezes the server clock to a fixed weekday evening so that
// schedule behaviour is deterministic regardless of the real day the suite
// runs on (otherwise the automatic weekend switch drops temporary parts on
// Sat/Sun), then reconciles state to the midweek schedule.
func pinMidweek(t *testing.T, srv *server) {
	t.Helper()
	fixed := time.Date(2026, 7, 8, 19, 45, 0, 0, time.UTC) // Wednesday, just after the 19:30 start
	srv.clock = func() time.Time { return fixed }
	srv.mu.Lock()
	srv.syncActiveScheduleLocked(fixed)
	srv.mu.Unlock()
}

func addTemporaryPart(t *testing.T, srv *server, mux http.Handler, title string) {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"seconds":300}`, title)
	req := httptest.NewRequest(http.MethodPost, "/api/control/adhoc-part", strings.NewReader(body))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK adhoc response, got %d: %s", res.Code, res.Body.String())
	}
}

func backdateTemporaryParts(srv *server, createdAt time.Time) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for i := range srv.talks {
		if srv.talks[i].Temporary {
			srv.talks[i].CreatedAt = createdAt
		}
	}
}

func TestStaleTemporaryPartPurgedWhenNextMeetingStarts(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	addTemporaryPart(t, srv, mux, "Previous congregation's announcement")
	backdateTemporaryParts(srv, time.Now().AddDate(0, 0, -8))

	state := srv.snapshot()
	for _, talk := range state.Schedule {
		if talk.Temporary {
			t.Fatalf("expected stale temporary part to be purged, got %+v", state.Schedule)
		}
	}
	found := false
	for _, talk := range state.Schedule {
		if talk.ID == state.CurrentTalkID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a valid current talk after purge, got id %d", state.CurrentTalkID)
	}
}

func TestTemporaryPartAddedDuringPrestartSurvivesMeetingStart(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	addTemporaryPart(t, srv, mux, "Opening announcement")
	sessionStart, ok := latestMeetingStart(time.Now(), srv.config.MeetingStarts)
	if !ok {
		t.Fatal("expected a recent meeting start in the default config")
	}
	backdateTemporaryParts(srv, sessionStart.Add(-time.Minute))

	state := srv.snapshot()
	hasTemp := false
	for _, talk := range state.Schedule {
		if talk.Temporary {
			hasTemp = true
			break
		}
	}
	if !hasTemp {
		t.Fatalf("expected prestart temporary part to survive the meeting start, got %+v", state.Schedule)
	}
}

func TestStaleTemporaryPartKeptWhileTimerRunning(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	addTemporaryPart(t, srv, mux, "Held over part")

	req := httptest.NewRequest(http.MethodPost, "/api/control/start", nil)
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK start response, got %d: %s", res.Code, res.Body.String())
	}

	backdateTemporaryParts(srv, time.Now().AddDate(0, 0, -8))

	state := srv.snapshot()
	hasTemp := false
	for _, talk := range state.Schedule {
		if talk.Temporary {
			hasTemp = true
			break
		}
	}
	if !hasTemp {
		t.Fatalf("expected temporary part to stay while the timer runs, got %+v", state.Schedule)
	}
}

func TestMergeTemporaryPartsKeepsTrailingTempWhenBaseShrinks(t *testing.T) {
	base := []Talk{{ID: 1, Title: "Part 1", Duration: 300}}
	existing := []Talk{
		{ID: 1, Title: "Old 1", Duration: 300},
		{ID: 2, Title: "Old 2", Duration: 300},
		{ID: 3, Title: "Old 3", Duration: 300},
		{ID: 4, Title: "Ad hoc", Duration: 300, Temporary: true},
	}
	merged := mergeTemporaryParts(base, existing)
	if len(merged) != 2 {
		t.Fatalf("expected base part plus temporary part, got %+v", merged)
	}
	if !merged[1].Temporary || merged[1].Title != "Ad hoc" {
		t.Fatalf("expected trailing temporary part to survive a shorter base schedule, got %+v", merged)
	}
}

func TestSetupResponsesReturnSavedScheduleNotRuntime(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	addTemporaryPart(t, srv, mux, "Live ad hoc part")

	req := httptest.NewRequest(http.MethodPost, "/api/template/midweek", nil)
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK template response, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	saved := append([]Talk(nil), srv.config.Schedule...)
	srv.mu.Unlock()
	if len(state.Schedule) != len(saved) {
		t.Fatalf("expected setup response to carry the saved schedule (%d parts), got %d: %+v", len(saved), len(state.Schedule), state.Schedule)
	}
	for _, talk := range state.Schedule {
		if talk.Temporary {
			t.Fatalf("expected no temporary parts in setup response, got %+v", state.Schedule)
		}
	}
}

func TestSaveConfigStripsTemporaryFlag(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	body := `{"deviceName":"Hall","schedule":[{"title":"Part 1","durationSeconds":300,"closingSeconds":60,"temporary":true}],"meetingStarts":[{"day":1,"time":"19:00"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK save response, got %d: %s", res.Code, res.Body.String())
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, talk := range srv.config.Schedule {
		if talk.Temporary {
			t.Fatalf("expected saved config schedule to have no temporary parts, got %+v", srv.config.Schedule)
		}
	}
}

func TestUnrelatedConfigSaveKeepsSelectionWithTemporaryPart(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	pinMidweek(t, srv)

	addTemporaryPart(t, srv, mux, "Staged part")
	srv.mu.Lock()
	stagedID := srv.state.CurrentTalkID
	config := srv.config
	srv.mu.Unlock()

	payload, err := json.Marshal(map[string]any{
		"deviceName":    "Renamed Hall",
		"meetingType":   config.MeetingType,
		"meetingStarts": config.MeetingStarts,
		"schedule":      config.Schedule,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(payload)))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK save response, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.CurrentTalkID != stagedID {
		t.Fatalf("expected selection to stay on part %d after unrelated config save, got %d", stagedID, state.CurrentTalkID)
	}
}

func TestMeetingTypeSwitchDropsTemporaryParts(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	addTemporaryPart(t, srv, mux, "Yesterday's part")

	srv.mu.Lock()
	if meetingTypeForTime(time.Now()) == "weekend" {
		srv.state.MeetingType = "midweek"
	} else {
		srv.state.MeetingType = "weekend"
	}
	srv.mu.Unlock()

	state := srv.snapshot()
	for _, talk := range state.Schedule {
		if talk.Temporary {
			t.Fatalf("expected temporary parts to be dropped on meeting-type switch, got %+v", state.Schedule)
		}
	}
}

func TestCircuitOverseerScheduleTransforms(t *testing.T) {
	// Weekend CO: three 30-minute parts.
	wk := scheduleForMeetingType("weekend", nil, true)
	if len(wk) != 3 {
		t.Fatalf("expected 3 weekend CO parts, got %d: %+v", len(wk), wk)
	}
	if wk[0].Title != "Public Talk" || wk[1].Title != "Watchtower Summary" || wk[2].Title != "Circuit Overseer Service Talk" {
		t.Fatalf("unexpected weekend CO titles: %+v", wk)
	}
	for _, p := range wk {
		if p.Duration != 1800 {
			t.Fatalf("expected 30-minute weekend CO parts, got %+v", p)
		}
	}

	// Midweek CO: Congregation Bible Study becomes the CO service talk, rest intact.
	base := defaultSchedule()
	mid := scheduleForMeetingType("midweek", base, true)
	if len(mid) != len(base) {
		t.Fatalf("expected midweek CO to keep part count %d, got %d", len(base), len(mid))
	}
	hasCO, hasCBS := false, false
	for _, p := range mid {
		if p.Title == "Circuit Overseer Service Talk" {
			hasCO = true
		}
		if strings.Contains(strings.ToLower(p.Title), "bible study") {
			hasCBS = true
		}
	}
	if !hasCO || hasCBS {
		t.Fatalf("midweek CO should replace Bible Study with CO talk: %+v", mid)
	}
	// The saved schedule must not be mutated.
	if strings.Contains(strings.ToLower(base[6].Title), "bible study") == false {
		t.Fatalf("base schedule was mutated: %+v", base)
	}

	// Off = unchanged.
	if len(scheduleForMeetingType("weekend", nil, false)) != 2 {
		t.Fatal("weekend without CO should be 2 parts")
	}
}

func TestCircuitOverseerEndpointPersistsAndSwaps(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	srv, err := newServer(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	pinMidweek(t, srv) // deterministic weekday

	req := httptest.NewRequest(http.MethodPost, "/api/control/circuit-overseer", strings.NewReader(`{"on":true}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d: %s", res.Code, res.Body.String())
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.CircuitOverseer {
		t.Fatal("expected circuitOverseer true in state")
	}
	for _, p := range state.Schedule {
		if strings.Contains(strings.ToLower(p.Title), "bible study") {
			t.Fatalf("midweek CO schedule should not contain Bible Study: %+v", state.Schedule)
		}
	}

	// Persisted to disk as a future expiry (still active).
	reloaded, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !circuitOverseerActive(reloaded.CircuitOverseerExpiresAt, srv.clock()) {
		t.Fatalf("expected circuitOverseer expiry persisted and active, got %v", reloaded.CircuitOverseerExpiresAt)
	}
}

func TestCircuitOverseerAutoExpiresAfterThreeHours(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	// Pin to a fixed weekday, turn CO on.
	base := time.Date(2026, 7, 8, 19, 0, 0, 0, time.UTC) // Wednesday 19:00
	srv.clock = func() time.Time { return base }
	srv.mu.Lock()
	srv.syncActiveScheduleLocked(base)
	srv.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/control/circuit-overseer", strings.NewReader(`{"on":true}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("toggle on returned %d: %s", res.Code, res.Body.String())
	}

	// Still active 2h later.
	srv.clock = func() time.Time { return base.Add(2 * time.Hour) }
	if !srv.snapshot().CircuitOverseer {
		t.Fatal("expected CO still active after 2h")
	}

	// Auto-deactivated 3h+ later, and the schedule reverts.
	srv.clock = func() time.Time { return base.Add(3*time.Hour + time.Minute) }
	st := srv.snapshot()
	if st.CircuitOverseer {
		t.Fatal("expected CO to auto-deactivate after 3 hours")
	}
	for _, p := range st.Schedule {
		if p.Title == "Circuit Overseer Service Talk" {
			t.Fatalf("expected schedule to revert after expiry, got %+v", st.Schedule)
		}
	}
}

func TestCircuitOverseerRejectedWhileRunning(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	pinMidweek(t, srv)

	do := func(path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res.Code
	}

	if code := do("/api/control/start", ""); code != http.StatusOK {
		t.Fatalf("start returned %d", code)
	}
	if code := do("/api/control/circuit-overseer", `{"on":true}`); code != http.StatusConflict {
		t.Fatalf("expected 409 while running, got %d", code)
	}
}

package main

import (
	"encoding/json"
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
	req.Host = "wallclock.local:8080"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing response, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "http://wallclock.local:8080/control?token=") {
		t.Fatalf("expected tokenized control URL, got %s", res.Body.String())
	}
}

func TestPairingEndpointUsesConfiguredPublicURL(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("http://wallclock.local:8080/control")
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
	if !strings.Contains(res.Body.String(), "http://wallclock.local:8080/control?token=") {
		t.Fatalf("expected configured public URL, got %s", res.Body.String())
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

func TestWeekendTemplate(t *testing.T) {
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
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
	if state.MeetingType != "weekend" {
		t.Fatalf("expected weekend meeting type, got %q", state.MeetingType)
	}
	if len(state.Schedule) != 2 {
		t.Fatalf("expected 2 weekend parts, got %d", len(state.Schedule))
	}
	if state.Schedule[0].Duration != 1800 || state.Schedule[1].Duration != 3600 {
		t.Fatalf("unexpected weekend schedule: %+v", state.Schedule)
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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A hall running two congregations off one box switches language between
// meetings. Before the pre-load that switch cost two live WOL fetches with the
// meeting waiting; the point of the sweep is that the second language is
// already cached, so the switch is served from config.
func TestPrefetchWarmsEveryLanguageInMeetingStarts(t *testing.T) {
	now := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC) // Wednesday morning, no meeting running
	srv, err := newServerWithClock(filepath.Join(t.TempDir(), "config.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	srv.config.AutoImportMidweek = true
	srv.config.MidweekLanguage = "en"
	srv.config.MeetingStarts = []MeetingStart{
		{ID: 1, Day: 2, Time: "19:00", Language: "en"},
		{ID: 2, Day: 4, Time: "19:00", Language: "tw"},
		// A weekend start runs the fixed local template and must not be fetched.
		{ID: 3, Day: 0, Time: "10:00", Language: "es"},
	}

	fetched := map[string]int{}
	originalFetch := fetchWOLPageFunc
	fetchWOLPageFunc = func(ctx context.Context, sourceURL string) (string, error) {
		switch {
		case strings.Contains(sourceURL, "/en/wol/meetings/"):
			fetched["en"]++
			return `<a href="/en/wol/d/r1/lp-e/202026111">Workbook</a>`, nil
		case strings.Contains(sourceURL, "/en/wol/d/r1/lp-e/202026111"):
			return `
				<h2>July 6-12</h2>
				<p>Opening Comments (1 min.)</p>
				<p>Digging for Spiritual Gems (10 min.)</p>
				<p>Bible Reading (4 min.)</p>
				<p>Concluding Comments (3 min.)</p>
			`, nil
		case strings.Contains(sourceURL, "/tw/wol/meetings/"):
			fetched["tw"]++
			return `<a href="/tw/wol/d/r33/lp-tw/202026222">Workbook</a>`, nil
		case strings.Contains(sourceURL, "/tw/wol/d/r33/lp-tw/202026222"):
			return `
				<h2>July 6-12</h2>
				<p>Ɔkasa (5 min.)</p>
				<p>Adwumayɛ mu nsɛm (10 min.)</p>
				<p>Kyerɛw kronkron akenkan (4 min.)</p>
				<p>Awiei nsɛm (1 min.)</p>
			`, nil
		case strings.Contains(sourceURL, "/es/"):
			fetched["es"]++
			return "", fmt.Errorf("weekend language must not be pre-loaded: %s", sourceURL)
		default:
			return "", fmt.Errorf("unexpected URL: %s", sourceURL)
		}
	}
	defer func() { fetchWOLPageFunc = originalFetch }()

	srv.prefetchMidweekLanguages(context.Background(), now)

	srv.mu.Lock()
	twCached := srv.midweekLanguageCachedLocked(now, "tw")
	esCached := srv.midweekLanguageCachedLocked(now, "es")
	srv.mu.Unlock()

	if !twCached {
		t.Fatal("expected the second congregation's Twi items to be pre-loaded")
	}
	if esCached || fetched["es"] > 0 {
		t.Fatal("expected the weekend start's language to be left alone")
	}

	// The switch must now be served from cache — no further network at all.
	fetchWOLPageFunc = func(ctx context.Context, sourceURL string) (string, error) {
		return "", fmt.Errorf("switch should have been served from cache, but fetched %s", sourceURL)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/control/midweek-language", strings.NewReader(`{"language":"tw"}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected an instant cached switch, got %d: %s", res.Code, res.Body.String())
	}
}

// Pre-loading must never change what is on screen; only importMidweekLanguage
// does that. A sweep that quietly switched the hall's language would be worse
// than the wait it removes.
func TestPrefetchLeavesTheActiveLanguageAlone(t *testing.T) {
	now := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	srv, err := newServerWithClock(filepath.Join(t.TempDir(), "config.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	srv.config.AutoImportMidweek = true
	srv.config.MidweekLanguage = "en"
	srv.config.MeetingStarts = []MeetingStart{{ID: 1, Day: 4, Time: "19:00", Language: "tw"}}

	originalFetch := fetchWOLPageFunc
	fetchWOLPageFunc = func(ctx context.Context, sourceURL string) (string, error) {
		switch {
		case strings.Contains(sourceURL, "/tw/wol/meetings/"):
			return `<a href="/tw/wol/d/r33/lp-tw/202026222">Workbook</a>`, nil
		case strings.Contains(sourceURL, "/tw/wol/d/r33/lp-tw/202026222"):
			return `
				<h2>July 6-12</h2>
				<p>Ɔkasa (5 min.)</p>
				<p>Adwumayɛ mu nsɛm (10 min.)</p>
				<p>Kyerɛw kronkron akenkan (4 min.)</p>
				<p>Awiei nsɛm (1 min.)</p>
			`, nil
		}
		return "", fmt.Errorf("unexpected URL: %s", sourceURL)
	}
	defer func() { fetchWOLPageFunc = originalFetch }()

	srv.prefetchMidweekLanguages(context.Background(), now)

	srv.mu.Lock()
	active := srv.config.MidweekLanguage
	override := srv.config.MidweekLanguageOverrideUntil
	srv.mu.Unlock()
	if active != "en" {
		t.Fatalf("pre-loading changed the active language to %q", active)
	}
	if !override.IsZero() {
		t.Fatal("pre-loading claimed an operator language override")
	}
}

// A language whose workbook is not published yet fails every sweep. Without a
// throttle the loop would retry it every 15 minutes, forever.
func TestPrefetchThrottlesRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	srv, err := newServerWithClock(filepath.Join(t.TempDir(), "config.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	srv.config.AutoImportMidweek = true
	srv.config.MidweekLanguage = "en"
	srv.config.MeetingStarts = []MeetingStart{{ID: 1, Day: 4, Time: "19:00", Language: "tw"}}

	attempts := 0
	originalFetch := fetchWOLPageFunc
	fetchWOLPageFunc = func(ctx context.Context, sourceURL string) (string, error) {
		attempts++
		return "", fmt.Errorf("not published yet")
	}
	defer func() { fetchWOLPageFunc = originalFetch }()

	srv.prefetchMidweekLanguages(context.Background(), now)
	first := attempts
	if first == 0 {
		t.Fatal("expected the first sweep to try")
	}

	srv.prefetchMidweekLanguages(context.Background(), now.Add(15*time.Minute))
	if attempts != first {
		t.Fatalf("expected a sweep 15 minutes later to be throttled, got %d more attempts", attempts-first)
	}

	srv.prefetchMidweekLanguages(context.Background(), now.Add(time.Hour+time.Minute))
	if attempts == first {
		t.Fatal("expected a retry once the hour had passed")
	}
}

// The weekend programme is a local template whose two titles come from the
// language alone, so switching it must not depend on WOL. It used to run the
// midweek path regardless: on a Sunday with flaky hall wifi, or before this
// week's workbook was published, the operator simply could not fix the language
// on screen.
func TestWeekendLanguageSwitchNeedsNoWorkbook(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC) // Sunday
	srv, err := newServerWithClock(filepath.Join(t.TempDir(), "config.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}

	originalFetch := fetchWOLPageFunc
	fetchWOLPageFunc = func(ctx context.Context, sourceURL string) (string, error) {
		return "", fmt.Errorf("wol unreachable: %s", sourceURL)
	}
	defer func() { fetchWOLPageFunc = originalFetch }()

	req := httptest.NewRequest(http.MethodPost, "/api/control/midweek-language", strings.NewReader(`{"language":"es"}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("weekend switch should not need a workbook, got %d: %s", res.Code, strings.TrimSpace(res.Body.String()))
	}

	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.MeetingType != "weekend" {
		t.Fatalf("expected the weekend meeting to stay active, got %q", state.MeetingType)
	}
	if len(state.Schedule) != 2 ||
		state.Schedule[0].Title != "Discurso público" ||
		state.Schedule[1].Title != "Estudio de La Atalaya" {
		t.Fatalf("expected the Spanish weekend template, got %+v", state.Schedule)
	}
}

// Switching the weekend language must not rewrite the midweek programme: it
// changes what is on screen now, not which workbook the hall imports.
func TestWeekendLanguageSwitchLeavesTheMidweekBaselineAlone(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC) // Sunday
	srv, err := newServerWithClock(filepath.Join(t.TempDir(), "config.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	srv.mu.Lock()
	srv.config.MidweekURL = "https://wol.jw.org/en/wol/d/r1/lp-e/202026111"
	srv.config.MidweekImportedWeek = isoWeekString(now)
	baseline := append([]Talk(nil), srv.config.Schedule...)
	_, _, ok, message := srv.applyWeekendLanguageLocked(now, "es")
	url := srv.config.MidweekURL
	week := srv.config.MidweekImportedWeek
	after := append([]Talk(nil), srv.config.Schedule...)
	srv.mu.Unlock()

	if !ok {
		t.Fatalf("expected the weekend switch to apply: %s", message)
	}
	if url != "https://wol.jw.org/en/wol/d/r1/lp-e/202026111" || week != isoWeekString(now) {
		t.Fatalf("weekend switch rewrote the midweek import state: %s / %s", url, week)
	}
	if len(after) != len(baseline) {
		t.Fatalf("weekend switch replaced the midweek baseline: %d parts, was %d", len(after), len(baseline))
	}
}

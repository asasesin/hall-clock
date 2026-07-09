package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const autoImportHour = 3

var defaultMidweekLanguageSources = map[string]string{
	"en": "https://wol.jw.org/en/wol/d/r1/lp-e/202026241",
	"es": "https://wol.jw.org/es/wol/d/r4/lp-s/202026241",
	"tw": "https://wol.jw.org/tw/wol/d/r33/lp-tw/202026241",
}

var fetchWOLPageFunc = fetchWOLPage

func importMidweekFromURL(ctx context.Context, sourceURL string) ([]Talk, error) {
	body, err := fetchWOLPageFunc(ctx, sourceURL)
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
	req.Header.Set("User-Agent", "hall-clock-local-appliance/0.1")

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

func wolLanguage(sourceURL string) string {
	if m := wolDocURLPattern.FindStringSubmatch(sourceURL); m != nil {
		return m[1]
	}
	return ""
}

func normalizeMidweekLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "en", "english":
		return "en"
	case "es", "spanish":
		return "es"
	case "tw", "twi":
		return "tw"
	default:
		return ""
	}
}

func (s *server) midweekLanguageSourceLocked(language string) (string, bool) {
	language = normalizeMidweekLanguage(language)
	if language == "" {
		return "", false
	}
	if source := strings.TrimSpace(s.config.MidweekLanguageSources[language]); source != "" {
		return source, true
	}
	for _, start := range s.config.MeetingStarts {
		if source := strings.TrimSpace(start.MidweekURL); wolLanguage(source) == language {
			return source, true
		}
	}
	if wolLanguage(s.config.MidweekURL) == language {
		return s.config.MidweekURL, true
	}
	if source := defaultMidweekLanguageSources[language]; source != "" {
		return source, true
	}
	return "", false
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
		now := s.clock()
		s.mu.Lock()
		enabled := s.config.AutoImportMidweek
		_, due := s.nextAutoImportSourceLocked(now)
		nextCheck := s.nextAutoImportCheckAtLocked(now)
		s.mu.Unlock()

		if enabled && due {
			s.autoImportTick(context.Background(), now)
			time.Sleep(time.Hour)
			continue
		}

		time.Sleep(time.Until(nextCheck))
	}
}

// autoImportTick pulls the current week's midweek program. The caller controls
// the Monday 3:00 AM schedule; this method still guards against duplicate
// imports and disabled auto-import settings.
func (s *server) autoImportTick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	enabled := s.config.AutoImportMidweek
	source, due := s.nextAutoImportSourceLocked(now)
	s.mu.Unlock()

	currentWeek := isoWeekString(now)
	if !enabled || !due {
		return
	}

	page, err := fetchWOLPageFunc(ctx, weeklyMeetingsURL(source.exampleURL, now))
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
	if err := validateImportedLanguage(wolLanguage(docURL), schedule); err != nil {
		log.Printf("auto-import: %v", err)
		return
	}

	s.mu.Lock()
	config, state, ok := s.applyAutoImportedScheduleLocked(now, source, docURL, schedule)
	s.mu.Unlock()
	if !ok {
		log.Printf("auto-import: skipped applying stale import from %s", docURL)
		return
	}

	if err := saveConfig(s.configPath, config); err != nil {
		log.Printf("auto-import: could not save config: %v", err)
	}
	s.broadcast(state)
	log.Printf("auto-import: applied midweek schedule for %s from %s", currentWeek, docURL)
}

func (s *server) applyAutoImportedScheduleLocked(now time.Time, source autoImportSource, docURL string, schedule []Talk) (Config, State, bool) {
	currentSource, due := s.nextAutoImportSourceLocked(now)
	if !due || !sameAutoImportSource(source, currentSource) {
		return Config{}, State{}, false
	}

	currentWeek := isoWeekString(now)
	s.config.MidweekURL = docURL
	s.config.MidweekLanguage = wolLanguage(docURL)
	s.config.MidweekImportedWeek = currentWeek
	if s.config.MidweekLanguageSources == nil {
		s.config.MidweekLanguageSources = map[string]string{}
	}
	if s.config.MidweekLanguage != "" {
		s.config.MidweekLanguageSources[s.config.MidweekLanguage] = docURL
	}
	s.storeMidweekLanguageScheduleLocked(s.config.MidweekLanguage, currentWeek, docURL, schedule)
	if source.startID != 0 {
		for i := range s.config.MeetingStarts {
			if s.config.MeetingStarts[i].ID == source.startID {
				s.config.MeetingStarts[i].MidweekURL = docURL
				s.config.MeetingStarts[i].MidweekImportedWeek = currentWeek
				break
			}
		}
	}
	s.config.Schedule = schedule
	s.applyActiveScheduleChangeLocked(now)
	return s.config, s.snapshotLocked(), true
}

func (s *server) storeMidweekLanguageScheduleLocked(language, importedWeek, sourceURL string, schedule []Talk) {
	language = normalizeMidweekLanguage(language)
	if language == "" || importedWeek == "" || len(schedule) == 0 {
		return
	}
	if s.config.MidweekLanguageSchedules == nil {
		s.config.MidweekLanguageSchedules = map[string]MidweekLanguageSchedule{}
	}
	cached := make([]Talk, len(schedule))
	copy(cached, schedule)
	normalizeSchedule(cached)
	s.config.MidweekLanguageSchedules[language] = MidweekLanguageSchedule{
		ImportedWeek: importedWeek,
		URL:          sourceURL,
		Schedule:     cached,
	}
}

func (s *server) applyCachedMidweekLanguageScheduleLocked(now time.Time, language string) (Config, State, bool, string) {
	language = normalizeMidweekLanguage(language)
	if language == "" {
		return Config{}, State{}, false, "unsupported language"
	}
	if s.state.Status != StatusIdle {
		return Config{}, State{}, false, "language can only be changed while idle"
	}
	cached, ok := s.config.MidweekLanguageSchedules[language]
	if !ok || cached.ImportedWeek != isoWeekString(now) || len(cached.Schedule) == 0 {
		return Config{}, State{}, false, fmt.Sprintf("%s parts are not imported for this week yet", languageName(language))
	}
	s.config.MeetingType = "midweek"
	s.config.MidweekURL = cached.URL
	s.config.MidweekLanguage = language
	s.config.MidweekImportedWeek = cached.ImportedWeek
	s.config.Schedule = append([]Talk(nil), cached.Schedule...)
	normalizeSchedule(s.config.Schedule)
	s.state.MidweekLanguage = language
	s.applyActiveScheduleChangeLocked(now)
	return s.config, s.snapshotLocked(), true, ""
}

// importMidweekLanguage fetches a language's parts from WOL and applies them.
// The caller must NOT hold s.mu: this blocks on the network, so it takes the
// lock only to read the source URL and again to apply the result.
func (s *server) importMidweekLanguage(ctx context.Context, now time.Time, language string) (Config, State, bool, string) {
	language = normalizeMidweekLanguage(language)
	if language == "" {
		return Config{}, State{}, false, "unsupported language"
	}
	// midweekLanguageSourceLocked reads s.config maps, which the auto-import
	// goroutine writes; reading them unlocked aborts the process.
	s.mu.Lock()
	sourceURL, ok := s.midweekLanguageSourceLocked(language)
	s.mu.Unlock()
	if !ok {
		return Config{}, State{}, false, fmt.Sprintf("%s parts are not available for this hall", languageName(language))
	}

	schedule, err := importMidweekFromURL(ctx, sourceURL)
	if err != nil {
		return Config{}, State{}, false, fmt.Sprintf("could not import %s parts: %v", languageName(language), err)
	}
	if err := validateImportedLanguage(language, schedule); err != nil {
		return Config{}, State{}, false, err.Error()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Status != StatusIdle {
		return Config{}, State{}, false, "language can only be changed while idle"
	}
	importedWeek := isoWeekString(now)
	s.config.MeetingType = "midweek"
	s.config.MidweekURL = sourceURL
	s.config.MidweekLanguage = language
	s.config.MidweekImportedWeek = importedWeek
	if s.config.MidweekLanguageSources == nil {
		s.config.MidweekLanguageSources = map[string]string{}
	}
	s.config.MidweekLanguageSources[language] = sourceURL
	s.storeMidweekLanguageScheduleLocked(language, importedWeek, sourceURL, schedule)
	s.config.Schedule = append([]Talk(nil), schedule...)
	s.applyActiveScheduleChangeLocked(now)
	return s.config, s.snapshotLocked(), true, ""
}

func sameAutoImportSource(a, b autoImportSource) bool {
	return a.exampleURL == b.exampleURL && a.startID == b.startID
}

func validateImportedLanguage(language string, schedule []Talk) error {
	language = normalizeMidweekLanguage(language)
	if language == "" || language == "en" {
		return nil
	}
	if looksLikeEnglishMidweekSchedule(schedule) {
		return fmt.Errorf("WOL returned English titles for %s", languageName(language))
	}
	return nil
}

func looksLikeEnglishMidweekSchedule(schedule []Talk) bool {
	englishTitles := map[string]struct{}{
		"opening comments":                     {},
		"spiritual gems":                       {},
		"bible reading":                        {},
		"starting a conversation":              {},
		"following up":                         {},
		"making disciples":                     {},
		"explaining your beliefs":              {},
		"talk":                                 {},
		"congregation bible study":             {},
		"concluding comments":                  {},
		"local needs":                          {},
		"living as christians":                 {},
		"apply yourself to the field ministry": {},
	}

	matches := 0
	for _, talk := range schedule {
		if _, ok := englishTitles[strings.ToLower(strings.TrimSpace(talk.Title))]; ok {
			matches++
		}
	}
	return matches >= 2
}

func languageName(language string) string {
	switch normalizeMidweekLanguage(language) {
	case "es":
		return "Spanish"
	case "tw":
		return "Twi"
	default:
		return "English"
	}
}

type autoImportSource struct {
	exampleURL   string
	importedWeek string
	startID      int
}

func (s *server) nextAutoImportSourceLocked(now time.Time) (autoImportSource, bool) {
	if !shouldAutoImportNow(now, s.config.AutoImportMidweek, "") {
		return autoImportSource{}, false
	}
	if s.currentMidweekMeetingActiveLocked(now) {
		return autoImportSource{}, false
	}

	start, ok := nextMidweekMeetingStart(now, s.config.MeetingStarts)
	if !ok {
		if s.config.MidweekImportedWeek == isoWeekString(now) {
			return autoImportSource{}, false
		}
		return autoImportSource{exampleURL: s.config.MidweekURL, importedWeek: s.config.MidweekImportedWeek}, true
	}

	source := autoImportSource{
		exampleURL:   start.MidweekURL,
		importedWeek: start.MidweekImportedWeek,
		startID:      start.ID,
	}
	if source.exampleURL == "" {
		source.exampleURL = s.config.MidweekURL
		source.importedWeek = s.config.MidweekImportedWeek
		source.startID = 0
	}
	if source.importedWeek == isoWeekString(now) {
		return source, false
	}
	return source, true
}

func (s *server) nextAutoImportCheckAtLocked(now time.Time) time.Time {
	if !s.config.AutoImportMidweek || now.Before(currentWeekAutoImportAt(now)) {
		return nextAutoImportAt(now)
	}

	nextCheck := now.Add(time.Hour)
	if _, startAt, ok := nextMidweekMeetingStartAt(now, s.config.MeetingStarts); ok && startAt.After(now) && startAt.Before(nextCheck) {
		nextCheck = startAt
	}
	if _, activeUntil, ok := s.currentMidweekMeetingWindowLocked(now); ok && activeUntil.After(now) && activeUntil.Before(nextCheck) {
		nextCheck = activeUntil
	}
	return nextCheck
}

func nextMidweekMeetingStart(now time.Time, starts []MeetingStart) (MeetingStart, bool) {
	start, _, ok := nextMidweekMeetingStartAt(now, starts)
	return start, ok
}

func nextMidweekMeetingStartAt(now time.Time, starts []MeetingStart) (MeetingStart, time.Time, bool) {
	weekStart := currentWeekAutoImportAt(now)
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, now.Location())

	var selected MeetingStart
	var selectedAt time.Time
	found := false
	for _, start := range starts {
		if start.Day < int(time.Monday) || start.Day > int(time.Friday) {
			continue
		}
		parsed, err := parseClockTime(start.Time)
		if err != nil {
			continue
		}
		startAt := weekStart.AddDate(0, 0, start.Day-int(time.Monday))
		startAt = time.Date(startAt.Year(), startAt.Month(), startAt.Day(), parsed.hour, parsed.minute, 0, 0, now.Location())
		if startAt.Before(now) {
			continue
		}
		if !found || startAt.Before(selectedAt) {
			selected = start
			selectedAt = startAt
			found = true
		}
	}
	return selected, selectedAt, found
}

func (s *server) currentMidweekMeetingActiveLocked(now time.Time) bool {
	_, _, ok := s.currentMidweekMeetingWindowLocked(now)
	return ok
}

func (s *server) currentMidweekMeetingWindowLocked(now time.Time) (time.Time, time.Time, bool) {
	var latestStart time.Time
	found := false
	for _, start := range s.config.MeetingStarts {
		if start.Day < int(time.Monday) || start.Day > int(time.Friday) || start.Day != int(now.Weekday()) {
			continue
		}
		parsed, err := parseClockTime(start.Time)
		if err != nil {
			continue
		}
		startAt := time.Date(now.Year(), now.Month(), now.Day(), parsed.hour, parsed.minute, 0, 0, now.Location())
		if !startAt.Before(now) {
			continue
		}
		if !found || startAt.After(latestStart) {
			latestStart = startAt
			found = true
		}
	}
	if !found {
		return time.Time{}, time.Time{}, false
	}

	activeUntil := latestStart.Add(circuitOverseerDuration)
	for _, start := range s.config.MeetingStarts {
		if start.Day < int(time.Monday) || start.Day > int(time.Friday) {
			continue
		}
		parsed, err := parseClockTime(start.Time)
		if err != nil {
			continue
		}
		startAt := time.Date(now.Year(), now.Month(), now.Day(), parsed.hour, parsed.minute, 0, 0, now.Location())
		if start.Day != int(now.Weekday()) || !startAt.After(latestStart) {
			continue
		}
		if startAt.Before(activeUntil) {
			activeUntil = startAt
		}
	}
	if !now.Before(activeUntil) {
		return time.Time{}, time.Time{}, false
	}
	return latestStart, activeUntil, true
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

	var talks []Talk
	seen := map[string]struct{}{}
	previousTitle := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		titleText, minutesText, ok := extractTiming(line)
		if !ok {
			if !looksLikeTimingDetail(line) {
				previousTitle = cleanTimingTitle(line)
			}
			continue
		}

		title := cleanTimingTitle(titleText)
		if title == "" {
			title = previousTitle
		}
		if title == "" {
			continue
		}
		minutes, err := parsePositiveInt(minutesText)
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
		previousTitle = ""
	}

	if len(talks) == 0 {
		return nil, errors.New("no timing slots found")
	}
	normalizeSchedule(talks)
	return talks, nil
}

var timingPattern = regexp.MustCompile(`(?i)\(\s*(?:(\d{1,3})\s*(?:min|mins|minutes|simma)\.?|(?:min|mins|minutes|simma)\.?\s*(\d{1,3}))\s*\)`)

func extractTiming(line string) (string, string, bool) {
	match := timingPattern.FindStringSubmatchIndex(line)
	if match == nil {
		return "", "", false
	}
	minutes := ""
	if match[2] >= 0 {
		minutes = line[match[2]:match[3]]
	} else {
		minutes = line[match[4]:match[5]]
	}
	return strings.TrimSpace(line[:match[0]]), minutes, true
}

func looksLikeTimingDetail(line string) bool {
	return strings.HasPrefix(line, "(") || strings.HasSuffix(line, ")")
}

func htmlToText(input string) string {
	replacements := []struct {
		old string
		new string
	}{
		{"</p>", "\n"},
		{"</h1>", "\n"},
		{"</h2>", "\n"},
		{"</h3>", "\n"},
		{"</h4>", "\n"},
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

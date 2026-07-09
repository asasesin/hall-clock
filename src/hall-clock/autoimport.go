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
	enabled := s.config.AutoImportMidweek
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
	s.applyActiveScheduleChangeLocked(now)
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

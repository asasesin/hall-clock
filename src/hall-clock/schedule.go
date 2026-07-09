package main

import (
	"strings"
	"time"
)

// circuitOverseerDuration is how long the CO-visit schedule stays active after
// the operator turns it on, so it applies to one meeting session and then
// clears itself (a box shared by congregations without a CO visit is unaffected).
const circuitOverseerDuration = 3 * time.Hour

// circuitOverseerActive reports whether CO mode is currently in effect.
func circuitOverseerActive(expiresAt time.Time, now time.Time) bool {
	return !expiresAt.IsZero() && now.Before(expiresAt)
}

// circuitOverseerExpiryPtr returns the expiry for the state snapshot, or nil
// when CO mode is not active (so the UI can show/omit a countdown).
func circuitOverseerExpiryPtr(expiresAt time.Time, now time.Time) *time.Time {
	if !circuitOverseerActive(expiresAt, now) {
		return nil
	}
	e := expiresAt
	return &e
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

// weekendTitles translates the weekend program. The weekend parts are fixed
// rather than imported from WOL, so every language has to be spelled out here.
var weekendTitles = map[string]struct {
	publicTalk        string
	watchtowerStudy   string
	watchtowerSummary string
}{
	"en": {"Public Talk", "Watchtower Study", "Watchtower Summary"},
	"es": {"Discurso público", "Estudio de La Atalaya", "Estudio de La Atalaya"},
	"tw": {"Baguam Kasa", "Ɔwɛn-Aban Adesua", "Ɔwɛn-Aban Adesua"},
}

func weekendTitlesFor(language string) struct {
	publicTalk        string
	watchtowerStudy   string
	watchtowerSummary string
} {
	if titles, ok := weekendTitles[normalizeMidweekLanguage(language)]; ok {
		return titles
	}
	return weekendTitles["en"]
}

func weekendSchedule(language string) []Talk {
	titles := weekendTitlesFor(language)
	return []Talk{
		{ID: 1, Title: titles.publicTalk, Duration: 1800, Closing: 300},
		{ID: 2, Title: titles.watchtowerStudy, Duration: 3600, Closing: 300},
	}
}

func meetingTypeForTime(now time.Time) string {
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return "weekend"
	}
	return "midweek"
}

func scheduleForMeetingType(meetingType string, midweekSchedule []Talk, circuitOverseer bool, midweekLanguage string) []Talk {
	if meetingType == "weekend" {
		if circuitOverseer {
			return circuitOverseerWeekendSchedule(midweekLanguage)
		}
		return weekendSchedule(midweekLanguage)
	}
	if circuitOverseer {
		return circuitOverseerMidweekSchedule(midweekSchedule)
	}
	return midweekSchedule
}

// congregationBibleStudyMarkers matches the Congregation Bible Study part by the
// title WOL publishes for it in each supported language, so CO mode finds the
// part to replace no matter which language the week was imported in.
var congregationBibleStudyMarkers = map[string]string{
	"en": "bible study",
	"es": "estudio bíblico",
	"tw": "bible adesua",
}

// serviceTalkTitles translates the service talk the CO gives in place of the
// midweek Congregation Bible Study. WOL never publishes this part, so unlike
// every other midweek title it cannot be imported.
var serviceTalkTitles = map[string]string{
	"en": "Service Talk",
	"es": "Discurso de servicio",
	"tw": "Ɔsom Kasa",
}

// congregationBibleStudyLanguage reports the language of a Congregation Bible
// Study title. The markers do not overlap, so at most one can match.
func congregationBibleStudyLanguage(title string) (string, bool) {
	lowered := strings.ToLower(strings.TrimSpace(title))
	for language, marker := range congregationBibleStudyMarkers {
		if strings.Contains(lowered, marker) {
			return language, true
		}
	}
	return "", false
}

func serviceTalkTitle(language string) string {
	if title := serviceTalkTitles[normalizeMidweekLanguage(language)]; title != "" {
		return title
	}
	return serviceTalkTitles["en"]
}

// circuitOverseerWeekendSchedule is the weekend program during a circuit
// overseer visit: a 30-minute public talk, a 30-minute Watchtower summary, and
// the CO's 30-minute service talk.
func circuitOverseerWeekendSchedule(language string) []Talk {
	titles := weekendTitlesFor(language)
	return []Talk{
		{ID: 1, Title: titles.publicTalk, Duration: 1800, Closing: 300},
		{ID: 2, Title: titles.watchtowerSummary, Duration: 1800, Closing: 300},
		{ID: 3, Title: serviceTalkTitle(language), Duration: 1800, Closing: 300},
	}
}

// circuitOverseerMidweekSchedule replaces the Congregation Bible Study with the
// CO's 30-minute service talk, leaving the rest of the midweek program intact.
// It returns a copy so the saved schedule is never mutated.
//
// The service talk is titled in the language of the part it replaces, not the
// language recorded in config. Those disagree whenever the config flag is stale
// (or was never written), and the schedule on the projector has to read as one
// language: an English program with one Spanish part in the middle is worse than
// an English program.
func circuitOverseerMidweekSchedule(base []Talk) []Talk {
	out := make([]Talk, 0, len(base))
	replaced := false
	for _, talk := range base {
		partLanguage, isBibleStudy := congregationBibleStudyLanguage(talk.Title)
		if !replaced && isBibleStudy {
			out = append(out, Talk{
				ID:       talk.ID,
				Title:    serviceTalkTitle(partLanguage),
				Duration: 1800,
				Closing:  talk.Closing,
			})
			replaced = true
			continue
		}
		out = append(out, talk)
	}
	return out
}

func sameSchedule(a, b []Talk) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isWeekendSchedule reports whether a saved schedule is really the weekend
// template, in any language — a midweek schedule must never be one of those.
func isWeekendSchedule(schedule []Talk) bool {
	for language := range weekendTitles {
		weekend := weekendSchedule(language)
		normalizeSchedule(weekend)
		if sameSchedule(schedule, weekend) {
			return true
		}
	}
	return false
}

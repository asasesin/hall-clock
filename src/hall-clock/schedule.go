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

func weekendSchedule() []Talk {
	return []Talk{
		{ID: 1, Title: "Public Talk", Duration: 1800, Closing: 300},
		{ID: 2, Title: "Watchtower Study", Duration: 3600, Closing: 300},
	}
}

func meetingTypeForTime(now time.Time) string {
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return "weekend"
	}
	return "midweek"
}

func scheduleForMeetingType(meetingType string, midweekSchedule []Talk, circuitOverseer bool) []Talk {
	if meetingType == "weekend" {
		if circuitOverseer {
			return circuitOverseerWeekendSchedule()
		}
		return weekendSchedule()
	}
	if circuitOverseer {
		return circuitOverseerMidweekSchedule(midweekSchedule)
	}
	return midweekSchedule
}

// circuitOverseerWeekendSchedule is the weekend program during a circuit
// overseer visit: a 30-minute public talk, a 30-minute Watchtower summary, and
// the CO's 30-minute service talk.
func circuitOverseerWeekendSchedule() []Talk {
	return []Talk{
		{ID: 1, Title: "Public Talk", Duration: 1800, Closing: 300},
		{ID: 2, Title: "Watchtower Summary", Duration: 1800, Closing: 300},
		{ID: 3, Title: "Circuit Overseer Service Talk", Duration: 1800, Closing: 300},
	}
}

// circuitOverseerMidweekSchedule replaces the Congregation Bible Study with the
// CO's 30-minute service talk, leaving the rest of the midweek program intact.
// It returns a copy so the saved schedule is never mutated.
func circuitOverseerMidweekSchedule(base []Talk) []Talk {
	out := make([]Talk, 0, len(base))
	replaced := false
	for _, talk := range base {
		if !replaced && strings.Contains(strings.ToLower(talk.Title), "bible study") {
			out = append(out, Talk{
				ID:       talk.ID,
				Title:    "Circuit Overseer Service Talk",
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

func isWeekendSchedule(schedule []Talk) bool {
	weekend := weekendSchedule()
	normalizeSchedule(weekend)
	return sameSchedule(schedule, weekend)
}

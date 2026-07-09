package main

import (
	"strings"
	"time"
)

// applyScheduleLocked swaps in a new schedule without killing an in-progress
// timer: while running or paused, the current talk keeps counting and an
// edited duration shifts the remaining time like a manual adjust would.
func (s *server) applyScheduleLocked(schedule []Talk) {
	merged := mergeTemporaryParts(schedule, s.talks)
	s.talks = merged
	s.state.Schedule = merged

	if s.state.Status == StatusIdle {
		for _, talk := range merged {
			if talk.ID != s.state.CurrentTalkID {
				continue
			}
			// Keep the operator's selection; refresh the part's metadata and
			// only reset the clock when the part's duration actually changed.
			s.state.CurrentTalkTitle = talk.Title
			s.state.ClosingSeconds = talk.Closing
			if talk.Duration != s.state.DurationSeconds {
				s.state.DurationSeconds = talk.Duration
				s.state.RemainingSeconds = talk.Duration
				s.state.ElapsedSeconds = 0
				s.state.OvertimeSeconds = 0
				s.remainingAt = talk.Duration
			}
			return
		}
		s.selectTalkLocked(merged[0].ID)
		return
	}
	for _, talk := range merged {
		if talk.ID != s.state.CurrentTalkID {
			continue
		}
		s.recalculateLocked(s.clock())
		if delta := talk.Duration - s.state.DurationSeconds; delta != 0 {
			s.state.DurationSeconds = talk.Duration
			s.state.RemainingSeconds += delta
			s.remainingAt = s.state.RemainingSeconds
			if s.state.Status == StatusRunning {
				s.startedAt = s.clock()
			}
		}
		s.state.CurrentTalkTitle = talk.Title
		s.state.ClosingSeconds = talk.Closing
		return
	}
	s.selectTalkLocked(merged[0].ID)
}

func mergeTemporaryParts(base []Talk, existing []Talk) []Talk {
	if len(existing) == 0 {
		return append([]Talk(nil), base...)
	}

	type temporaryPart struct {
		index int
		talk  Talk
	}

	var temps []temporaryPart
	for i, talk := range existing {
		if talk.Temporary {
			temps = append(temps, temporaryPart{index: i, talk: talk})
		}
	}
	if len(temps) == 0 {
		return append([]Talk(nil), base...)
	}

	out := make([]Talk, 0, len(base)+len(temps))
	tempIdx := 0
	for i := 0; i <= len(base); i++ {
		for tempIdx < len(temps) && temps[tempIdx].index <= i {
			out = append(out, temps[tempIdx].talk)
			tempIdx++
		}
		if i < len(base) {
			out = append(out, base[i])
		}
	}
	// Temps recorded past the end of a shorter base schedule still belong in
	// the meeting: keep them at the end instead of dropping them.
	for ; tempIdx < len(temps); tempIdx++ {
		out = append(out, temps[tempIdx].talk)
	}
	return out
}

// withoutTemporaryTalks returns talks with ad-hoc parts removed; used when the
// active meeting type switches, since parts added for one meeting have no
// place in the other meeting's schedule.
func withoutTemporaryTalks(talks []Talk) []Talk {
	kept := make([]Talk, 0, len(talks))
	for _, talk := range talks {
		if !talk.Temporary {
			kept = append(kept, talk)
		}
	}
	return kept
}

// sameBaseSchedule reports whether the non-temporary talks in talks match
// base, so live ad-hoc parts don't make an unchanged schedule look changed.
func sameBaseSchedule(talks []Talk, base []Talk) bool {
	i := 0
	for _, talk := range talks {
		if talk.Temporary {
			continue
		}
		if i >= len(base) || talk != base[i] {
			return false
		}
		i++
	}
	return i == len(base)
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

// syncCircuitOverseerLocked reconciles the effective CO flag with its expiry —
// auto-deactivating the mode once 3 hours have passed so it never carries over
// to another congregation's meeting on a shared box. Rebuilds the schedule only
// while idle, so a running meeting is never disturbed mid-part.
func (s *server) syncCircuitOverseerLocked(now time.Time) {
	active := circuitOverseerActive(s.config.CircuitOverseerExpiresAt, now)
	s.state.CircuitOverseerExpiresAt = circuitOverseerExpiryPtr(s.config.CircuitOverseerExpiresAt, now)
	if active == s.state.CircuitOverseer {
		return
	}
	if s.state.Status != StatusIdle {
		return
	}
	s.state.CircuitOverseer = active
	s.applyScheduleLocked(scheduleForMeetingType(meetingTypeForTime(now), s.config.Schedule, active))
}

func (s *server) syncActiveScheduleLocked(now time.Time) {
	if s.state.Status != StatusIdle {
		return
	}
	activeMeetingType := meetingTypeForTime(now)
	if s.state.MeetingType == activeMeetingType {
		return
	}
	s.state.MeetingType = activeMeetingType
	s.talks = withoutTemporaryTalks(s.talks)
	s.applyScheduleLocked(scheduleForMeetingType(activeMeetingType, s.config.Schedule, s.state.CircuitOverseer))
}

// purgeStaleTemporaryPartsLocked drops ad-hoc parts left over from an earlier
// meeting session. A part created before the most recent configured meeting
// start (minus the prestart window, so parts added while preparing for a
// meeting count as part of it) belongs to a previous congregation's meeting
// and is removed. Runs only while idle so an in-progress timer is never
// disturbed.
func (s *server) purgeStaleTemporaryPartsLocked(now time.Time) {
	if s.state.Status != StatusIdle {
		return
	}
	hasTemporary := false
	for _, talk := range s.talks {
		if talk.Temporary {
			hasTemporary = true
			break
		}
	}
	if !hasTemporary {
		return
	}
	sessionStart, ok := latestMeetingStart(now, s.config.MeetingStarts)
	if !ok {
		return
	}
	cutoff := sessionStart.Add(-time.Duration(s.config.PrestartSeconds) * time.Second)

	kept := make([]Talk, 0, len(s.talks))
	removed := false
	for _, talk := range s.talks {
		if talk.Temporary && talk.CreatedAt.Before(cutoff) {
			removed = true
			continue
		}
		kept = append(kept, talk)
	}
	if !removed || len(kept) == 0 {
		return
	}
	s.talks = kept
	s.state.Schedule = kept
	for _, talk := range kept {
		if talk.ID == s.state.CurrentTalkID {
			return
		}
	}
	s.selectTalkLocked(kept[0].ID)
}

// latestMeetingStart returns the most recent configured meeting start at or
// before now, looking back up to a week.
func latestMeetingStart(now time.Time, starts []MeetingStart) (time.Time, bool) {
	for dayOffset := 0; dayOffset < 8; dayOffset++ {
		day := now.AddDate(0, 0, -dayOffset)
		weekday := int(day.Weekday())
		var best time.Time
		found := false
		for _, slot := range starts {
			if slot.Day != weekday {
				continue
			}
			hourMinute, err := parseClockTime(slot.Time)
			if err != nil {
				continue
			}
			start := time.Date(day.Year(), day.Month(), day.Day(), hourMinute.hour, hourMinute.minute, 0, 0, now.Location())
			if start.After(now) {
				continue
			}
			if !found || start.After(best) {
				best = start
				found = true
			}
		}
		if found {
			return best, true
		}
	}
	return time.Time{}, false
}

func (s *server) applyActiveScheduleChangeLocked(now time.Time) {
	activeMeetingType := meetingTypeForTime(now)
	activeSchedule := scheduleForMeetingType(activeMeetingType, s.config.Schedule, s.state.CircuitOverseer)
	if s.state.Status == StatusIdle {
		if s.state.MeetingType != activeMeetingType {
			s.state.MeetingType = activeMeetingType
			s.talks = withoutTemporaryTalks(s.talks)
			s.applyScheduleLocked(activeSchedule)
		} else if !sameBaseSchedule(s.talks, activeSchedule) {
			s.applyScheduleLocked(activeSchedule)
		}
		return
	}
	if s.state.MeetingType == activeMeetingType && activeMeetingType == "midweek" {
		s.applyScheduleLocked(activeSchedule)
	}
}

func (s *server) recalculateLocked(now time.Time) {
	s.syncCircuitOverseerLocked(now)
	s.syncActiveScheduleLocked(now)
	s.purgeStaleTemporaryPartsLocked(now)
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

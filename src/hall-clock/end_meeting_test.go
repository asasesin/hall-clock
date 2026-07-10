package main

import (
	"testing"
	"time"
)

// Ending a meeting stops the clock, releases the running part to idle, and wipes
// the overtime tally.
func TestEndMeetingIdlesAndClearsOvertime(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(90 * time.Second)
	h.post("/api/control/next", "")
	h.runPartOver(30 * time.Second)
	if got := h.state().MeetingOvertimeSeconds; got != 120 {
		t.Fatalf("setup: want 120s over, got %d", got)
	}

	end := h.post("/api/control/end", "")
	if end.Status != StatusIdle {
		t.Fatalf("end meeting must idle the clock, got %s", end.Status)
	}
	if end.MeetingOvertimeSeconds != 0 {
		t.Fatalf("end meeting must wipe the tally, got %ds", end.MeetingOvertimeSeconds)
	}
	if len(h.srv.retiredOverruns) != 0 {
		t.Fatalf("end meeting must drop the overtime records, got %+v", h.srv.retiredOverruns)
	}
	if end.RemainingSeconds != end.DurationSeconds {
		t.Fatalf("end meeting should leave the part at full time, got remaining=%d duration=%d",
			end.RemainingSeconds, end.DurationSeconds)
	}
}

// The whole point: after ending, the next meeting's prestart countdown appears,
// which a timer left running would have suppressed.
func TestEndMeetingRestoresPrestartForNextMeeting(t *testing.T) {
	h := newOverrideHarness(t) // Thursday 19:00, prestart 300s

	// Run the last part well over and leave it, the way a meeting really ends.
	h.runPartOver(200 * time.Second)

	// Move to the next midweek meeting's prestart window with the timer left
	// running: no countdown, because prestart only shows while idle.
	h.now = time.Date(2026, 7, 13, 18, 57, 0, 0, time.UTC) // Monday, inside 18:55-19:00
	if st := h.state(); st.PrestartActive {
		t.Fatalf("precondition: a running timer should suppress prestart, got active=%v", st.PrestartActive)
	}

	// End the meeting; the countdown returns.
	end := h.post("/api/control/end", "")
	if !end.PrestartActive {
		t.Fatalf("prestart countdown should appear once the meeting is ended, got active=%v status=%s",
			end.PrestartActive, end.Status)
	}
	if end.PrestartRemaining <= 0 || end.PrestartRemaining > 300 {
		t.Fatalf("prestart remaining out of range: %ds", end.PrestartRemaining)
	}
}

// Ending an already-idle clock is a harmless no-op (the button is disabled for
// it, but the endpoint must not misbehave if called anyway).
func TestEndMeetingWhileIdleIsHarmless(t *testing.T) {
	h := newOverrideHarness(t)
	if st := h.state(); st.Status != StatusIdle {
		t.Fatalf("precondition: expected idle, got %s", st.Status)
	}
	end := h.post("/api/control/end", "")
	if end.Status != StatusIdle || end.MeetingOvertimeSeconds != 0 {
		t.Fatalf("ending while idle changed something: status=%s behind=%d", end.Status, end.MeetingOvertimeSeconds)
	}
}

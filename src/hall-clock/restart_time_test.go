package main

import (
	"testing"
	"time"
)

// Restart time refills the current part's clock but keeps it running, and it
// keeps counting from the top.
func TestRestartTimeKeepsRunning(t *testing.T) {
	h := newOverrideHarness(t)
	full := h.state().DurationSeconds

	h.post("/api/control/start", "")
	h.advance(30 * time.Second)
	if before := h.state(); before.Status != StatusRunning || before.RemainingSeconds != full-30 {
		t.Fatalf("setup: status=%s remaining=%d", before.Status, before.RemainingSeconds)
	}

	h.post("/api/control/reset", "")
	just := h.state()
	if just.Status != StatusRunning {
		t.Fatalf("restart must not stop the clock, got %s", just.Status)
	}
	if just.RemainingSeconds != full {
		t.Fatalf("restart must refill the clock to %ds, got %ds", full, just.RemainingSeconds)
	}

	// It resumes counting down from full, not from where it was.
	h.advance(10 * time.Second)
	if got := h.state().RemainingSeconds; got != full-10 {
		t.Fatalf("clock did not resume from the top, got %ds want %ds", got, full-10)
	}
}

// Restarting a paused part refills it but leaves it paused.
func TestRestartTimeWhilePausedStaysPaused(t *testing.T) {
	h := newOverrideHarness(t)
	full := h.state().DurationSeconds

	h.post("/api/control/start", "")
	h.advance(20 * time.Second)
	h.post("/api/control/pause", "")

	h.post("/api/control/reset", "")
	st := h.state()
	if st.Status != StatusPaused {
		t.Fatalf("restart while paused must stay paused, got %s", st.Status)
	}
	if st.RemainingSeconds != full {
		t.Fatalf("restart must refill to %ds, got %ds", full, st.RemainingSeconds)
	}
	// Paused: the clock does not move.
	h.advance(15 * time.Second)
	if got := h.state().RemainingSeconds; got != full {
		t.Fatalf("paused clock moved after restart, got %ds", got)
	}
}

// Restarting a part that had run over gives its time back, so the meeting total
// drops by that live amount and the part is no longer over.
func TestRestartTimeGivesBackOvertime(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(40 * time.Second)
	if got := h.state().MeetingOvertimeSeconds; got != 40 {
		t.Fatalf("setup: want 40s over, got %d", got)
	}

	h.post("/api/control/reset", "")
	st := h.state()
	if st.MeetingOvertimeSeconds != 0 {
		t.Fatalf("restart should give the overtime back, got %ds", st.MeetingOvertimeSeconds)
	}
	if st.OvertimeSeconds != 0 || st.Status != StatusRunning {
		t.Fatalf("expected a running part no longer over, got status=%s overtime=%d", st.Status, st.OvertimeSeconds)
	}
}

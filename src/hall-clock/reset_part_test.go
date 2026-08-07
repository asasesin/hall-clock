package main

import (
	"fmt"
	"testing"
	"time"
)

// The Reset part button posts /api/control/select with the item already
// current, not /api/control/reset — that route is an alias for Next and would
// advance the meeting. These pin the behaviour the button depends on.

func TestReselectingCurrentPartRestartsIt(t *testing.T) {
	h := newOverrideHarness(t)

	before := h.state()
	h.post("/api/control/start", "")
	h.advance(45 * time.Second)
	if got := h.state().RemainingSeconds; got >= before.RemainingSeconds {
		t.Fatalf("setup: part did not run down, remaining %ds", got)
	}

	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, before.CurrentTalkID))

	after := h.state()
	if after.CurrentTalkID != before.CurrentTalkID {
		t.Fatalf("reset moved to talk %d (%q); it must stay on the current part",
			after.CurrentTalkID, after.CurrentTalkTitle)
	}
	if after.RemainingSeconds != before.DurationSeconds {
		t.Fatalf("reset left %ds remaining, want the full %ds",
			after.RemainingSeconds, before.DurationSeconds)
	}
	if after.ElapsedSeconds != 0 {
		t.Fatalf("reset left %ds elapsed, want 0", after.ElapsedSeconds)
	}
	if after.Status != StatusIdle {
		t.Fatalf("reset left the clock %q, want idle — a restarted part waits for Start", after.Status)
	}
}

// A false start's seconds belong to nobody: the operator is restarting this
// part, not leaving it, so they must not be banked the way Next banks them.
// This is the whole reason the button cannot just call Next.
func TestReselectingCurrentPartDoesNotBankOvertime(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(90 * time.Second)
	current := h.state().CurrentTalkID
	if got := h.state().MeetingOvertimeSeconds; got != 90 {
		t.Fatalf("setup: expected the live overtime to show, got %ds", got)
	}

	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, current))

	if got := h.state().MeetingOvertimeSeconds; got != 0 {
		t.Fatalf("restarting a part banked %ds of overtime; the meeting must not be charged for a false start", got)
	}
}

// Reset stays available on the final part, where Next refuses.
func TestReselectingCurrentPartWorksOnTheFinalPart(t *testing.T) {
	h := newOverrideHarness(t)

	schedule := h.state().Schedule
	last := schedule[len(schedule)-1]
	h.selectPart(last.ID)
	h.post("/api/control/start", "")
	h.advance(20 * time.Second)

	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, last.ID))

	after := h.state()
	if after.CurrentTalkID != last.ID {
		t.Fatalf("reset on the last part moved to talk %d", after.CurrentTalkID)
	}
	if after.RemainingSeconds != last.Duration {
		t.Fatalf("reset left %ds remaining, want %ds", after.RemainingSeconds, last.Duration)
	}
}

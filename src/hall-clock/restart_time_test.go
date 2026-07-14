package main

import (
	"testing"
	"time"
)

// The legacy reset route backs the Stop timer control: it retires the current
// live item, moves to the next schedule item, and leaves that item staged idle.
func TestStopTimerAdvancesAndStagesIdle(t *testing.T) {
	h := newOverrideHarness(t)
	schedule := h.state().Schedule
	if len(schedule) < 2 {
		t.Fatal("need at least two parts")
	}

	h.post("/api/control/start", "")
	h.advance(30 * time.Second)

	h.post("/api/control/reset", "")
	st := h.state()
	if st.Status != StatusIdle {
		t.Fatalf("stop timer must stage the next item idle, got %s", st.Status)
	}
	if st.CurrentTalkID != schedule[1].ID {
		t.Fatalf("expected next item %d, got %d", schedule[1].ID, st.CurrentTalkID)
	}
	if st.RemainingSeconds != schedule[1].Duration || st.ElapsedSeconds != 0 {
		t.Fatalf("next item not staged fresh: remaining=%d elapsed=%d", st.RemainingSeconds, st.ElapsedSeconds)
	}
}

// Stopping a paused item is still leaving that item, so it stages the next item
// instead of refilling the current one.
func TestStopTimerWhilePausedAdvances(t *testing.T) {
	h := newOverrideHarness(t)
	schedule := h.state().Schedule
	if len(schedule) < 2 {
		t.Fatal("need at least two parts")
	}

	h.post("/api/control/start", "")
	h.advance(20 * time.Second)
	h.post("/api/control/pause", "")

	h.post("/api/control/reset", "")
	st := h.state()
	if st.Status != StatusIdle || st.CurrentTalkID != schedule[1].ID {
		t.Fatalf("expected paused stop to stage next item idle, got status=%s talk=%d", st.Status, st.CurrentTalkID)
	}
}

// Stopping a part that ran over banks its overtime, just like Next part.
func TestStopTimerBanksOvertime(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(40 * time.Second)
	if got := h.state().MeetingOvertimeSeconds; got != 40 {
		t.Fatalf("setup: want 40s over, got %d", got)
	}

	h.post("/api/control/reset", "")
	st := h.state()
	if st.MeetingOvertimeSeconds != 40 {
		t.Fatalf("stop timer should bank overtime, got %ds", st.MeetingOvertimeSeconds)
	}
	if st.OvertimeSeconds != 0 || st.Status != StatusIdle {
		t.Fatalf("expected next item staged idle without live overtime, got status=%s overtime=%d", st.Status, st.OvertimeSeconds)
	}
}

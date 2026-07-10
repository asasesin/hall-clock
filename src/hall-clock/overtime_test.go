package main

import (
	"fmt"
	"testing"
	"time"
)

// runPartOver runs the current part until it is `over` seconds past its time.
func (h *overrideHarness) runPartOver(over time.Duration) {
	h.t.Helper()
	remaining := time.Duration(h.state().RemainingSeconds) * time.Second
	h.post("/api/control/start", "")
	h.advance(remaining + over)
}

func TestMeetingOvertimeAccumulatesAcrossParts(t *testing.T) {
	h := newOverrideHarness(t)

	if got := h.state().MeetingOvertimeSeconds; got != 0 {
		t.Fatalf("a fresh meeting is not behind, got %ds", got)
	}

	// Part 1 runs 90 seconds long. While it is still running the total already
	// reflects it: the chairman should not have to wait for the part to end.
	h.runPartOver(90 * time.Second)
	if got := h.state().MeetingOvertimeSeconds; got != 90 {
		t.Fatalf("live overtime not counted, got %ds want 90", got)
	}

	// Moving on banks it.
	h.post("/api/control/next", "")
	if got := h.state().MeetingOvertimeSeconds; got != 90 {
		t.Fatalf("overtime not banked on advance, got %ds want 90", got)
	}

	// Part 2 runs 30 seconds long: the totals add.
	h.runPartOver(30 * time.Second)
	if got := h.state().MeetingOvertimeSeconds; got != 120 {
		t.Fatalf("overtime did not accumulate, got %ds want 120", got)
	}
	h.post("/api/control/next", "")
	if got := h.state().MeetingOvertimeSeconds; got != 120 {
		t.Fatalf("banked total wrong after second part, got %ds want 120", got)
	}
}

// Time saved does not pay back time lost.
func TestMeetingOvertimeIgnoresUndertime(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(60 * time.Second)
	h.post("/api/control/next", "")

	// Part 2 finishes two minutes early.
	h.post("/api/control/start", "")
	h.advance(30 * time.Second)
	if rem := h.state().RemainingSeconds; rem <= 0 {
		t.Fatalf("expected the part to still have time left, got %ds", rem)
	}
	h.post("/api/control/next", "")

	if got := h.state().MeetingOvertimeSeconds; got != 60 {
		t.Fatalf("finishing early paid back overtime, got %ds want 60", got)
	}
}

// Restarting the part you are on is not leaving it, so nothing is banked twice.
func TestMeetingOvertimeNotBankedOnRestartOrReselect(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(45 * time.Second)
	current := h.state().CurrentTalkID

	// Restarting the time gives the current part its full clock back, so its
	// live overtime disappears rather than being banked.
	h.post("/api/control/reset", "")
	if got := h.state().MeetingOvertimeSeconds; got != 0 {
		t.Fatalf("restart banked the current part's overtime, got %ds want 0", got)
	}

	// Re-selecting the same part is also a restart.
	h.runPartOver(45 * time.Second)
	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, current))
	if got := h.state().MeetingOvertimeSeconds; got != 0 {
		t.Fatalf("re-selecting the current part banked its overtime, got %ds want 0", got)
	}
}

// Skipping ahead with the picker still retires the part being left.
func TestMeetingOvertimeBankedWhenSkippingWithPicker(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(75 * time.Second)
	schedule := h.state().Schedule
	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, schedule[3].ID))

	if got := h.state().MeetingOvertimeSeconds; got != 75 {
		t.Fatalf("skipping ahead lost the overtime, got %ds want 75", got)
	}
}

// A rejected Next at the end of the schedule must not bank anything.
func TestMeetingOvertimeNotBankedWhenAdvanceRejected(t *testing.T) {
	h := newOverrideHarness(t)

	schedule := h.state().Schedule
	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, schedule[len(schedule)-1].ID))
	h.runPartOver(50 * time.Second)

	before := h.state().MeetingOvertimeSeconds
	h.postExpect("/api/control/next", "", 409)
	after := h.state().MeetingOvertimeSeconds

	if before != 50 || after != 50 {
		t.Fatalf("a refused advance changed the total: before=%d after=%d want 50/50", before, after)
	}
}

// The total belongs to one meeting and clears itself for the next.
func TestMeetingOvertimeClearsOnNextMeeting(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(120 * time.Second)
	h.post("/api/control/next", "")
	if got := h.state().MeetingOvertimeSeconds; got != 120 {
		t.Fatalf("setup: want 120, got %d", got)
	}

	// Back to idle, then on to the next day's meeting.
	h.post("/api/control/reset", "")
	h.advance(24 * time.Hour)

	if got := h.state().MeetingOvertimeSeconds; got != 0 {
		t.Fatalf("last meeting's overtime carried into the next one, got %ds", got)
	}
}

// A meeting in progress must never have its total zeroed under it.
func TestMeetingOvertimeSurvivesWithinTheSameMeeting(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(60 * time.Second)
	h.post("/api/control/next", "")

	// Hours pass, still the same meeting session, timer left running.
	h.post("/api/control/start", "")
	h.advance(2 * time.Hour)

	if got := h.state().MeetingOvertimeSeconds; got < 60 {
		t.Fatalf("banked overtime was cleared during the meeting, got %ds", got)
	}
}

// Only a person leaving a part banks its overtime. Schedule rebuilds reselect
// parts internally, and none of those are the operator finishing a talk.
func TestMeetingOvertimeNotBankedByScheduleRebuild(t *testing.T) {
	h := newOverrideHarness(t)

	h.runPartOver(45 * time.Second)
	before := h.state().MeetingOvertimeSeconds
	if before != 45 {
		t.Fatalf("setup: want 45s live overtime, got %ds", before)
	}

	// Saving an edited schedule mid-meeting rebuilds the running talks.
	h.saveEditedSchedule(120)

	// The part is still the one being spoken: nothing has been retired.
	if len(h.srv.retiredOverruns) != 0 {
		t.Fatalf("a schedule rebuild retired %d parts: %+v", len(h.srv.retiredOverruns), h.srv.retiredOverruns)
	}
}

// The total is derived from per-part records, so a retired part's overrun can be
// identified and given back — the thing a running sum could never support.
func TestMeetingOvertimeRecordsWhichPartRanOver(t *testing.T) {
	h := newOverrideHarness(t)

	first := h.state().CurrentTalkID
	h.runPartOver(90 * time.Second)
	h.post("/api/control/next", "")
	second := h.state().CurrentTalkID
	h.runPartOver(30 * time.Second)
	h.post("/api/control/next", "")

	got := h.srv.retiredOverruns
	want := []partOverrun{{talkID: first, seconds: 90}, {talkID: second, seconds: 30}}
	if len(got) != len(want) {
		t.Fatalf("got %d retired parts, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retired[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if total := h.state().MeetingOvertimeSeconds; total != 120 {
		t.Fatalf("total should be the sum of the records, got %ds want 120", total)
	}
}

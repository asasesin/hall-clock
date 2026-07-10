package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func (h *overrideHarness) postExpect(path, body string, want int) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("X-Wall-Clock-Token", h.srv.config.ControlToken)
	res := httptest.NewRecorder()
	h.mux.ServeHTTP(res, req)
	if res.Code != want {
		h.t.Fatalf("%s returned %d, want %d: %s", path, res.Code, want, res.Body.String())
	}
	return res
}

func (h *overrideHarness) selectPart(talkID int) {
	h.t.Helper()
	h.post("/api/control/select", fmt.Sprintf(`{"talkId":%d}`, talkID))
}

// The meeting is a list, not a loop: Next on the final part must refuse rather
// than silently restarting the meeting at the opening comments.
func TestNextStopsAtEndOfSchedule(t *testing.T) {
	h := newOverrideHarness(t)

	schedule := h.state().Schedule
	last := schedule[len(schedule)-1]
	h.selectPart(last.ID)

	res := h.postExpect("/api/control/next", "", http.StatusConflict)

	after := h.state()
	if after.CurrentTalkID != last.ID {
		t.Fatalf("Next past the last item moved to talk %d (%q); it must stay put",
			after.CurrentTalkID, after.CurrentTalkTitle)
	}
	if after.CurrentTalkID == schedule[0].ID && len(schedule) > 1 {
		t.Fatal("Next wrapped around to the first item")
	}
	if res.Body.Len() == 0 {
		t.Fatal("expected an explanatory error body")
	}
}

// Symmetrically, Previous on the first part must not jump to the last.
func TestPreviousStopsAtStartOfSchedule(t *testing.T) {
	h := newOverrideHarness(t)

	schedule := h.state().Schedule
	first := schedule[0]
	h.selectPart(first.ID)

	h.postExpect("/api/control/previous", "", http.StatusConflict)

	if after := h.state(); after.CurrentTalkID != first.ID {
		t.Fatalf("Previous before the first item moved to talk %d (%q)",
			after.CurrentTalkID, after.CurrentTalkTitle)
	}
}

// Advancing through the middle of the schedule still works, and staging a part
// leaves the timer idle so the operator presses Start when the speaker begins.
func TestNextAdvancesAndStagesIdle(t *testing.T) {
	h := newOverrideHarness(t)

	schedule := h.state().Schedule
	if len(schedule) < 2 {
		t.Fatal("need at least two parts")
	}
	h.selectPart(schedule[0].ID)
	h.post("/api/control/start", "")
	h.advance(30e9) // 30s into the part

	req := httptest.NewRequest(http.MethodPost, "/api/control/next", nil)
	req.Header.Set("X-Wall-Clock-Token", h.srv.config.ControlToken)
	res := httptest.NewRecorder()
	h.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("next returned %d: %s", res.Code, res.Body.String())
	}
	var state State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}

	if state.CurrentTalkID != schedule[1].ID {
		t.Fatalf("expected to advance to talk %d, got %d", schedule[1].ID, state.CurrentTalkID)
	}
	if state.Status != StatusIdle {
		t.Fatalf("advancing must stage the next part idle, got %s", state.Status)
	}
	if state.RemainingSeconds != schedule[1].Duration || state.ElapsedSeconds != 0 {
		t.Fatalf("next part not staged fresh: remaining=%d elapsed=%d", state.RemainingSeconds, state.ElapsedSeconds)
	}
}

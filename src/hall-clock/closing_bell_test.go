package main

import (
	"encoding/json"
	"testing"
)

// The importer and the save path must agree on what a closing bell is, or a
// saved schedule would drift away from the imported one part at a time.
func TestDerivedClosingSecondsMatchesImportFormula(t *testing.T) {
	cases := map[int]int{1: 30, 2: 60, 3: 90, 4: 120, 10: 120, 30: 120}
	for minutes, want := range cases {
		if got := derivedClosingSeconds(minutes); got != want {
			t.Errorf("derivedClosingSeconds(%d) = %d, want %d", minutes, got, want)
		}
	}
}

func TestApplyImportedClosingSeconds(t *testing.T) {
	baseline := []Talk{
		{ID: 1, Title: "Opening Comments", Duration: 60, Closing: 30},
		{ID: 2, Title: "Treasures From God's Word", Duration: 600, Closing: 120},
		{ID: 3, Title: "Concluding Comments", Duration: 180, Closing: 60},
	}

	cases := []struct {
		name     string
		schedule []Talk
		want     []int
	}{
		{
			// The operator cannot type these, but a crafted request can.
			name: "posted closing bells are ignored in favour of the import",
			schedule: []Talk{
				{Title: "Opening Comments", Duration: 60, Closing: 55},
				{Title: "Treasures From God's Word", Duration: 600, Closing: 5},
				{Title: "Concluding Comments", Duration: 180, Closing: 179},
			},
			want: []int{30, 120, 60},
		},
		{
			// Concluding Comments is 60s in the baseline, not the formula's 90.
			// Matching by title is what preserves that.
			name: "a baseline bell that differs from the formula survives",
			schedule: []Talk{
				{Title: "Concluding Comments", Duration: 180, Closing: 0},
			},
			want: []int{60},
		},
		{
			name: "an unknown part falls back to the import formula",
			schedule: []Talk{
				{Title: "Local Needs", Duration: 300, Closing: 999},
			},
			want: []int{120},
		},
		{
			// Treasures carries a 120s bell; shortened to 1 minute that bell
			// would cover the whole part and paint it amber start to finish.
			name: "a bell that no longer fits the shortened part is re-derived",
			schedule: []Talk{
				{Title: "Treasures From God's Word", Duration: 60, Closing: 120},
			},
			want: []int{30},
		},
		{
			name: "title match ignores case and surrounding space",
			schedule: []Talk{
				{Title: "  opening comments ", Duration: 60, Closing: 0},
			},
			want: []int{30},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applyImportedClosingSeconds(tc.schedule, baseline)
			for i, want := range tc.want {
				if got := tc.schedule[i].Closing; got != want {
					t.Errorf("part %d (%q): closing = %d, want %d", i, tc.schedule[i].Title, got, want)
				}
				if tc.schedule[i].Closing >= tc.schedule[i].Duration {
					t.Errorf("part %d: closing %d covers the whole %ds part", i, tc.schedule[i].Closing, tc.schedule[i].Duration)
				}
			}
		})
	}
}

// End to end: a request that tries to set its own closing bell is overruled.
func TestSaveConfigIgnoresClientClosingSeconds(t *testing.T) {
	h := newOverrideHarness(t)

	config := Config{
		DeviceName: "Hall Clock", MeetingType: "midweek", MeetingStartTime: "19:00",
		MeetingStarts: defaultMeetingStarts("19:00"), PrestartSeconds: 300,
		Schedule: []Talk{
			// Baseline has Opening Comments at 60s/30. Keep the duration, try to
			// move the bell to 59 seconds.
			{Title: "Opening Comments", Duration: 60, Closing: 59},
			{Title: "Treasures From God's Word", Duration: 600, Closing: 600},
		},
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	got := h.post("/api/config", string(body))

	if got.Schedule[0].Closing != 30 {
		t.Fatalf("client redefined the closing bell: got %ds, want the imported 30s", got.Schedule[0].Closing)
	}
	if got.Schedule[1].Closing != 120 {
		t.Fatalf("client redefined the closing bell: got %ds, want the imported 120s", got.Schedule[1].Closing)
	}
}

// A save that touches only the bell is not an edit, so it must not create a
// session-scoped override.
func TestClosingBellOnlyChangeIsNotAnEdit(t *testing.T) {
	h := newOverrideHarness(t)

	baseline := append([]Talk(nil), h.srv.config.Schedule...)
	tampered := append([]Talk(nil), baseline...)
	for i := range tampered {
		tampered[i].Closing = 7
	}

	config := Config{
		DeviceName: "Hall Clock", MeetingType: "midweek", MeetingStartTime: "19:00",
		MeetingStarts: defaultMeetingStarts("19:00"), PrestartSeconds: 300,
		Schedule: tampered,
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	h.post("/api/config", string(body))

	if len(h.srv.config.ScheduleOverride) != 0 {
		t.Fatalf("a rejected closing-bell change created an override: %+v", h.srv.config.ScheduleOverride)
	}
	if !sameSchedule(h.srv.config.Schedule, baseline) {
		t.Fatalf("baseline changed: %+v", h.srv.config.Schedule)
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.snapshot())
}

func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// One clock reading for the whole response: two would let MeetingType and
	// Schedule be resolved on opposite sides of an expiry boundary.
	now := s.clock()
	s.mu.Lock()
	out := Config{
		DeviceName:               s.config.DeviceName,
		AdvertisedBaseURL:        s.config.AdvertisedBaseURL,
		MeetingType:              meetingTypeForTime(now),
		MeetingStartTime:         s.config.MeetingStartTime,
		MeetingStarts:            append([]MeetingStart(nil), s.config.MeetingStarts...),
		PrestartSeconds:          s.config.PrestartSeconds,
		MidweekURL:               s.config.MidweekURL,
		MidweekLanguage:          s.config.MidweekLanguage,
		MidweekLanguageSources:   copyStringMap(s.config.MidweekLanguageSources),
		MidweekLanguageSchedules: copyMidweekLanguageScheduleMap(s.config.MidweekLanguageSchedules),
		AutoImportMidweek:        s.config.AutoImportMidweek,
		MidweekImportedWeek:      s.config.MidweekImportedWeek,
		// Load the editor with the program that is actually running, so a save
		// from this form can never post a schedule the clock is not using.
		Schedule: append([]Talk(nil), s.effectiveMidweekScheduleLocked(now)...),
	}
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.state.Status != StatusRunning {
		s.startedAt = s.clock()
		s.state.Status = StatusRunning
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handlePause(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.recalculateLocked(s.clock())
	if s.state.Status == StatusRunning {
		s.remainingAt = s.state.RemainingSeconds
		s.state.Status = StatusPaused
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

// handleReset restarts the current part's countdown from its full time without
// stopping it: a running part keeps running from the top, a paused one stays
// paused at the top. It does not change which part is selected. For a part that
// was over, this gives its time back, so the meeting's overtime total drops by
// that live amount.
func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.remainingAt = s.state.DurationSeconds
	s.state.RemainingSeconds = s.state.DurationSeconds
	s.state.ElapsedSeconds = 0
	s.state.OvertimeSeconds = 0
	// Restart the clock from now so a running part counts down from full; leaving
	// startedAt untouched would make recalculate immediately subtract the elapsed
	// time again.
	if s.state.Status == StatusRunning {
		s.startedAt = s.clock()
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleNext(w http.ResponseWriter, r *http.Request) {
	s.changeTalk(w, 1)
}

func (s *server) handlePrevious(w http.ResponseWriter, r *http.Request) {
	s.changeTalk(w, -1)
}

func (s *server) handleAdjust(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeltaSeconds int `json:"deltaSeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.recalculateLocked(s.clock())
	s.state.DurationSeconds = max(60, s.state.DurationSeconds+body.DeltaSeconds)
	s.state.RemainingSeconds = max(-3600, s.state.RemainingSeconds+body.DeltaSeconds)
	s.remainingAt = s.state.RemainingSeconds
	if s.state.Status == StatusRunning {
		s.startedAt = s.clock()
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleSetTime(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Seconds int `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	seconds := clamp(body.Seconds, 60, 7200)

	s.mu.Lock()
	if s.state.Status != StatusIdle {
		s.mu.Unlock()
		http.Error(w, "time can only be edited while idle", http.StatusConflict)
		return
	}
	s.state.DurationSeconds = seconds
	s.state.RemainingSeconds = seconds
	s.state.ElapsedSeconds = 0
	s.state.OvertimeSeconds = 0
	s.remainingAt = seconds
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleSelect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TalkID int `json:"talkId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	now := s.clock()
	s.mu.Lock()
	s.recalculateLocked(now)
	// Re-selecting the current part is a restart, not a departure, and a request
	// for a part that does not exist leaves nothing behind.
	if body.TalkID != s.state.CurrentTalkID && s.hasTalkLocked(body.TalkID) {
		s.retireCurrentPartLocked(now)
	}
	ok := s.selectTalkLocked(body.TalkID)
	state := s.snapshotLocked()
	s.mu.Unlock()

	if !ok {
		http.Error(w, "talk not found", http.StatusNotFound)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleAdhocPart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title   string `json:"title"`
		Seconds int    `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "Additional item"
	}
	seconds := clamp(body.Seconds, 60, 7200)

	s.mu.Lock()
	insertAt := len(s.talks)
	for i, talk := range s.talks {
		if talk.ID == s.state.CurrentTalkID {
			insertAt = i + 1
			break
		}
	}
	nextID := 1
	for _, talk := range s.talks {
		nextID = max(nextID, talk.ID+1)
	}
	part := Talk{ID: nextID, Title: title, Duration: seconds, Closing: min(60, seconds), Temporary: true, CreatedAt: s.clock()}
	s.talks = append(s.talks, Talk{})
	copy(s.talks[insertAt+1:], s.talks[insertAt:])
	s.talks[insertAt] = part
	s.state.Schedule = s.talks
	if s.state.Status == StatusIdle {
		s.selectTalkLocked(part.ID)
	}
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleMovePart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TalkID int `json:"talkId"`
		Delta  int `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Delta != -1 && body.Delta != 1 {
		http.Error(w, "delta must be -1 or 1", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	idx := -1
	for i, talk := range s.talks {
		if talk.ID == body.TalkID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		http.Error(w, "talk not found", http.StatusNotFound)
		return
	}
	if !s.talks[idx].Temporary {
		s.mu.Unlock()
		http.Error(w, "only temporary items can be moved here", http.StatusConflict)
		return
	}
	next := idx + body.Delta
	if next < 0 || next >= len(s.talks) {
		s.mu.Unlock()
		http.Error(w, "cannot move item further", http.StatusConflict)
		return
	}
	s.talks[idx], s.talks[next] = s.talks[next], s.talks[idx]
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleBell(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.bellSeq++
	s.state.Bell = s.bellSeq
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleCircuitOverseer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	now := s.clock()
	s.mu.Lock()
	// CO mode reshapes the whole schedule, so only allow it while idle — a
	// running/paused meeting would flip the flag without cleanly rebuilding.
	if s.state.Status != StatusIdle {
		s.mu.Unlock()
		http.Error(w, "circuit overseer mode can only be changed while idle", http.StatusConflict)
		return
	}
	// Turning it on sets a 3-hour expiry so it applies to this meeting session
	// only; turning it off clears it.
	if body.On {
		s.config.CircuitOverseerExpiresAt = now.Add(circuitOverseerDuration)
	} else {
		s.config.CircuitOverseerExpiresAt = time.Time{}
	}
	s.state.CircuitOverseer = circuitOverseerActive(s.config.CircuitOverseerExpiresAt, now)
	// Rebuild the active schedule for the new mode (swaps CO parts in/out).
	s.applyActiveScheduleChangeLocked(now)
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save config", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleMidweekLanguage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	language := normalizeMidweekLanguage(body.Language)
	if language == "" {
		http.Error(w, "unsupported language", http.StatusBadRequest)
		return
	}

	now := s.clock()
	s.mu.Lock()
	config, state, ok, message := s.applyCachedMidweekLanguageScheduleLocked(now, language)
	s.mu.Unlock()
	if !ok && strings.Contains(message, "not imported for this week yet") {
		config, state, ok, message = s.importMidweekLanguage(r.Context(), now, language)
	}
	if !ok {
		http.Error(w, message, http.StatusConflict)
		return
	}

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save language schedule", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)
}

func (s *server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var body Config
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(body.Schedule) == 0 {
		http.Error(w, "schedule cannot be empty", http.StatusBadRequest)
		return
	}

	normalizeSchedule(body.Schedule)
	body.DeviceName = strings.TrimSpace(body.DeviceName)
	if body.DeviceName == "" {
		body.DeviceName = "Hall Clock"
	}
	body.MeetingType = normalizeMeetingType(body.MeetingType)
	body.MeetingStartTime = normalizeStartTime(body.MeetingStartTime)
	body.MeetingStarts = normalizeMeetingStarts(body.MeetingStarts, body.MeetingStartTime)
	advertisedURL, err := normalizeAdvertisedControlURL(body.AdvertisedBaseURL)
	if err != nil {
		http.Error(w, "invalid advertisedBaseUrl", http.StatusBadRequest)
		return
	}
	if body.PrestartSeconds == 0 {
		body.PrestartSeconds = 300
	}
	body.PrestartSeconds = clamp(body.PrestartSeconds, 60, 1800)

	s.mu.Lock()
	// Read under the lock: the auto-import goroutine writes this map, and an
	// unsynchronized iteration of it aborts the process.
	existingLanguageSchedules := copyMidweekLanguageScheduleMap(s.config.MidweekLanguageSchedules)
	s.config.DeviceName = body.DeviceName
	s.config.AdvertisedBaseURL = advertisedURL
	s.config.MeetingType = body.MeetingType
	s.config.MeetingStartTime = body.MeetingStartTime
	s.config.MeetingStarts = preserveMeetingStartImportState(s.config.MeetingStarts, body.MeetingStarts)
	s.config.PrestartSeconds = body.PrestartSeconds
	s.config.MidweekURL = strings.TrimSpace(body.MidweekURL)
	if language := wolLanguage(s.config.MidweekURL); language != "" {
		s.config.MidweekLanguage = language
		if s.config.MidweekLanguageSources == nil {
			s.config.MidweekLanguageSources = map[string]string{}
		}
		s.config.MidweekLanguageSources[language] = s.config.MidweekURL
	}
	s.config.AutoImportMidweek = body.AutoImportMidweek
	// The closing bell belongs to the import, not to whoever posted this request.
	// Restore it against the baseline before anything compares schedules, so a
	// client that sent a stale or invented bell cannot make an unchanged program
	// look edited.
	applyImportedClosingSeconds(body.Schedule, s.config.Schedule)
	// A hand-edited schedule is an override scoped to this meeting session, not a
	// new baseline: it expires so the next congregation on a shared box starts
	// from the imported program. Saving the baseline back clears the override,
	// and re-saving an unchanged edit keeps its original expiry instead of
	// silently extending it (an unrelated setting change must not buy 3 more hours).
	saveNow := s.clock()
	switch {
	case sameSchedule(body.Schedule, s.config.Schedule):
		s.config.ScheduleOverride = nil
		s.config.ScheduleOverrideExpiresAt = time.Time{}
	case s.scheduleOverrideAppliesLocked(saveNow) && sameSchedule(body.Schedule, s.config.ScheduleOverride):
		// Unchanged edit: leave ScheduleOverrideExpiresAt alone. Testing "applies"
		// rather than "window still open" is what stops a stale browser tab from
		// re-arming an already-expired edit for another three hours.
	default:
		s.config.ScheduleOverride = body.Schedule
		s.config.ScheduleOverrideExpiresAt = saveNow.Add(scheduleOverrideDuration)
	}
	if len(body.MidweekLanguageSchedules) > 0 {
		s.config.MidweekLanguageSchedules = copyMidweekLanguageScheduleMap(body.MidweekLanguageSchedules)
	} else {
		s.config.MidweekLanguageSchedules = existingLanguageSchedules
	}
	s.state.DeviceName = body.DeviceName
	s.state.MeetingStartTime = body.MeetingStartTime
	s.state.MeetingStarts = body.MeetingStarts
	s.state.PrestartLabel = ""
	s.state.PrestartSeconds = body.PrestartSeconds
	s.applyActiveScheduleChangeLocked(s.clock())
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save config", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, state)

	now := s.clock()
	s.mu.Lock()
	_, due := s.nextAutoImportSourceLocked(now)
	// An import installs a new baseline and drops the override. Never let the
	// save that just created an override be the thing that triggers the import
	// which discards it: the operator would watch their edit revert seconds
	// after saving. The weekly Monday import still runs on its own schedule.
	if s.scheduleOverrideAppliesLocked(now) {
		due = false
	}
	s.mu.Unlock()
	if due {
		go s.autoImportTick(context.Background(), now)
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyMidweekLanguageScheduleMap(in map[string]MidweekLanguageSchedule) map[string]MidweekLanguageSchedule {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]MidweekLanguageSchedule, len(in))
	for key, value := range in {
		value.Schedule = append([]Talk(nil), value.Schedule...)
		out[key] = value
	}
	return out
}

func preserveMeetingStartImportState(existing, incoming []MeetingStart) []MeetingStart {
	byID := map[int]MeetingStart{}
	for _, start := range existing {
		if start.ID != 0 {
			byID[start.ID] = start
		}
	}
	for i := range incoming {
		previous, ok := byID[incoming[i].ID]
		if !ok || incoming[i].MidweekImportedWeek != "" || incoming[i].MidweekURL != previous.MidweekURL {
			continue
		}
		incoming[i].MidweekImportedWeek = previous.MidweekImportedWeek
	}
	return incoming
}

func (s *server) handleImportMidweek(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string `json:"url"`
		Apply bool   `json:"apply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	sourceURL := strings.TrimSpace(body.URL)
	if sourceURL == "" {
		http.Error(w, "midweek URL is required", http.StatusBadRequest)
		return
	}

	schedule, err := importMidweekFromURL(r.Context(), sourceURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !body.Apply {
		writeJSON(w, map[string]any{
			"meetingType": "midweek",
			"sourceUrl":   sourceURL,
			"schedule":    schedule,
		})
		return
	}

	s.mu.Lock()
	s.config.MeetingType = "midweek"
	s.config.MidweekURL = sourceURL
	if language := wolLanguage(sourceURL); language != "" {
		s.config.MidweekLanguage = language
		if s.config.MidweekLanguageSources == nil {
			s.config.MidweekLanguageSources = map[string]string{}
		}
		s.config.MidweekLanguageSources[language] = sourceURL
	}
	importedWeek := isoWeekString(s.clock())
	s.config.MidweekImportedWeek = importedWeek
	s.setBaselineScheduleLocked(schedule)
	s.storeMidweekLanguageScheduleLocked(s.config.MidweekLanguage, importedWeek, sourceURL, schedule)
	s.applyActiveScheduleChangeLocked(s.clock())
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save imported schedule", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, setupResponse(state, config, s.clock()))
}

func (s *server) handleImportMidweekText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text  string `json:"text"`
		Apply bool   `json:"apply"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	schedule, err := parseMidweekTimings(body.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !body.Apply {
		writeJSON(w, map[string]any{
			"meetingType": "midweek",
			"schedule":    schedule,
		})
		return
	}

	s.mu.Lock()
	s.config.MeetingType = "midweek"
	s.config.MidweekLanguage = ""
	s.config.MidweekImportedWeek = isoWeekString(s.clock())
	s.setBaselineScheduleLocked(schedule)
	s.applyActiveScheduleChangeLocked(s.clock())
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save imported schedule", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, setupResponse(state, config, s.clock()))
}

// setupResponse reshapes a state snapshot for the setup page: it carries the
// saved (editable) schedule and meeting type instead of the runtime ones,
// which on weekends resolve to the fixed weekend template. Without this, the
// setup editor would load the weekend parts and a subsequent Save would
// overwrite the saved midweek schedule with them.
func setupResponse(state State, config Config, now time.Time) State {
	state.MeetingType = config.MeetingType
	// Resolve against the status in the snapshot, not a fresh read, so the setup
	// page and the broadcast state always agree about which program is running.
	state.Schedule = append([]Talk(nil), effectiveMidweekSchedule(config, state.Status, now)...)
	return state
}

func (s *server) handleWeekendTemplate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	language := s.config.MidweekLanguage
	s.mu.Unlock()
	s.applyTemplate(w, "weekend", weekendSchedule(language))
}

func (s *server) handleMidweekTemplate(w http.ResponseWriter, r *http.Request) {
	s.applyTemplate(w, "midweek", defaultSchedule())
}

func (s *server) applyTemplate(w http.ResponseWriter, meetingType string, schedule []Talk) {
	normalizeSchedule(schedule)

	s.mu.Lock()
	s.config.MeetingType = meetingType
	if meetingType == "weekend" && !hasWeekendStart(s.config.MeetingStarts) {
		starts := append(s.config.MeetingStarts, MeetingStart{Day: int(time.Sunday), Time: "10:00"})
		s.config.MeetingStarts = normalizeMeetingStarts(starts, s.config.MeetingStartTime)
		s.state.MeetingStarts = s.config.MeetingStarts
	}
	if meetingType == "midweek" {
		s.setBaselineScheduleLocked(schedule)
	}
	s.applyActiveScheduleChangeLocked(s.clock())
	config := s.config
	state := s.snapshotLocked()
	s.mu.Unlock()

	if err := saveConfig(s.configPath, config); err != nil {
		http.Error(w, "could not save template", http.StatusInternalServerError)
		return
	}

	s.broadcast(state)
	writeJSON(w, setupResponse(state, config, s.clock()))
}

func (s *server) changeTalk(w http.ResponseWriter, delta int) {
	now := s.clock()
	s.mu.Lock()
	// Recalculate before reading s.talks, never after: it purges stale ad-hoc
	// parts and can swap the whole schedule, so an index taken beforehand may not
	// survive it.
	s.recalculateLocked(now)
	idx := 0
	for i, talk := range s.talks {
		if talk.ID == s.state.CurrentTalkID {
			idx = i
			break
		}
	}
	// A meeting is a list, not a loop. Advancing past the last item used to wrap
	// silently back to the opening comments, which is never what an operator
	// means at the end of the program.
	next := idx + delta
	if next < 0 || next >= len(s.talks) {
		s.mu.Unlock()
		http.Error(w, "no further item in the schedule", http.StatusConflict)
		return
	}
	// The operator is leaving this part, so whatever it ran over is the meeting's.
	s.retireCurrentPartLocked(now)
	s.selectTalkLocked(s.talks[next].ID)
	state := s.snapshotLocked()
	s.mu.Unlock()

	s.broadcast(state)
	writeJSON(w, state)
}

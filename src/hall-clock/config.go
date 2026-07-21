package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{AutoImportMidweek: true}, nil
	}
	if err != nil {
		return Config{}, err
	}
	// An empty file is a config that was being written when the box lost power
	// or the process was killed. Treat it like a missing one.
	if len(bytes.TrimSpace(data)) == 0 {
		log.Printf("config: %s is empty; starting from defaults", path)
		return Config{AutoImportMidweek: true}, nil
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		// A clock that will not boot is worse than a clock that forgot its
		// schedule: an unreadable config used to crash-loop the appliance until
		// somebody could reach it with ssh, which for most halls is nobody.
		// Keep the bad file for forensics and come up on defaults. Phones
		// re-pair themselves through /api/pairing, so a lost token is invisible.
		corrupt := path + ".corrupt"
		if renameErr := os.Rename(path, corrupt); renameErr != nil {
			log.Printf("config: could not set aside unreadable %s: %v", path, renameErr)
		}
		log.Printf("config: %s is unreadable (%v); starting from defaults, bad copy kept at %s", path, err, corrupt)
		return Config{AutoImportMidweek: true}, nil
	}
	return config, nil
}

// saveConfig writes the config atomically: a temporary file in the same
// directory, flushed to disk, then renamed over the target. os.WriteFile would
// truncate the real file first, so a process killed mid-write (systemd stopping
// the unit for an update, or the Pi losing power) would leave an empty config
// behind and the app would never boot again.
func saveConfig(path string, config Config) error {
	data, err := marshalConfig(config)
	if err != nil {
		return err
	}
	return writeConfigFile(path, data)
}

func marshalConfig(config Config) ([]byte, error) {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeConfigFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Rename is atomic, but on an SD card the rename can land before the data
	// does. Flush first, or a power cut leaves a valid name pointing at zeroes.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// persistConfig snapshots the current config under s.mu and writes it to disk.
// Writers are serialized and sequenced so a slower, older snapshot can never
// rename over a newer one — the marshal happens under s.mu, so the snapshot
// can't observe a half-applied mutation either.
func (s *server) persistConfig() error {
	s.mu.Lock()
	s.saveSeq++
	seq := s.saveSeq
	data, err := marshalConfig(s.config)
	s.mu.Unlock()
	if err != nil {
		return err
	}

	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if seq <= s.lastSavedSeq {
		// A newer snapshot already reached disk while we waited.
		return nil
	}
	if err := writeConfigFile(s.configPath, data); err != nil {
		return err
	}
	s.lastSavedSeq = seq
	return nil
}

func defaultConfigPath() string {
	if path := os.Getenv("WALL_CLOCK_CONFIG"); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "hall-clock", "config.json")
	}
	return "hall-clock.json"
}

func normalizeMeetingType(meetingType string) string {
	switch strings.ToLower(strings.TrimSpace(meetingType)) {
	case "weekend":
		return "weekend"
	default:
		return "midweek"
	}
}

func normalizeStartTime(startTime string) string {
	startTime = strings.TrimSpace(startTime)
	if startTime == "" {
		return "19:00"
	}
	parsed, err := parseClockTime(startTime)
	if err != nil {
		return "19:00"
	}
	return fmt.Sprintf("%02d:%02d", parsed.hour, parsed.minute)
}

func parseClockTime(value string) (clockTime, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return clockTime{}, errors.New("invalid time")
	}

	hour, err := parsePositiveInt(parts[0])
	if err != nil {
		return clockTime{}, err
	}
	minute, err := parsePositiveInt(parts[1])
	if err != nil {
		return clockTime{}, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return clockTime{}, errors.New("invalid time")
	}
	return clockTime{hour: hour, minute: minute}, nil
}

func normalizeMeetingStarts(starts []MeetingStart, fallbackStartTime string) []MeetingStart {
	if len(starts) == 0 {
		return defaultMeetingStarts(fallbackStartTime)
	}

	normalized := make([]MeetingStart, 0, len(starts))
	for _, start := range starts {
		if start.Day < int(time.Sunday) || start.Day > int(time.Saturday) {
			continue
		}
		start.Time = normalizeStartTime(start.Time)
		start.Congregation = strings.TrimSpace(start.Congregation)
		start.MidweekURL = strings.TrimSpace(start.MidweekURL)
		if language := normalizeMidweekLanguage(start.Language); language != "" {
			start.Language = language
		} else {
			start.Language = normalizeMidweekLanguage(wolLanguage(start.MidweekURL))
		}
		normalized = append(normalized, start)
	}
	if len(normalized) == 0 {
		return defaultMeetingStarts(fallbackStartTime)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Day != normalized[j].Day {
			return normalized[i].Day < normalized[j].Day
		}
		return normalized[i].Time < normalized[j].Time
	})
	for i := range normalized {
		normalized[i].ID = i + 1
	}
	return normalized
}

func defaultMeetingStarts(startTime string) []MeetingStart {
	startTime = normalizeStartTime(startTime)
	starts := make([]MeetingStart, 0, 6)
	starts = append(starts, MeetingStart{ID: 1, Day: int(time.Sunday), Time: "10:00"})
	for day := int(time.Monday); day <= int(time.Friday); day++ {
		starts = append(starts, MeetingStart{
			ID:           len(starts) + 1,
			Day:          day,
			Time:         startTime,
			Congregation: "",
		})
	}
	return starts
}

func hasWeekendStart(starts []MeetingStart) bool {
	for _, start := range starts {
		if start.Day == int(time.Sunday) || start.Day == int(time.Saturday) {
			return true
		}
	}
	return false
}

func normalizeSchedule(schedule []Talk) {
	for i := range schedule {
		schedule[i].ID = i + 1
		schedule[i].Title = strings.TrimSpace(schedule[i].Title)
		if schedule[i].Title == "" {
			schedule[i].Title = fmt.Sprintf("Part %d", i+1)
		}
		schedule[i].Duration = clamp(schedule[i].Duration, 60, 7200)
		schedule[i].Closing = clamp(schedule[i].Closing, 0, schedule[i].Duration)
		// Config schedules are never temporary; a temporary part that leaks
		// into a save would otherwise be silently purged at runtime while
		// still sitting in the config file.
		schedule[i].Temporary = false
		schedule[i].CreatedAt = time.Time{}
	}
}

func clamp(value, minValue, maxValue int) int {
	return min(max(value, minValue), maxValue)
}

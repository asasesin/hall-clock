package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func saveConfig(path string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
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

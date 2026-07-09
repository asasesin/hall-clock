package main

import "time"

type TimerStatus string

const (
	StatusIdle    TimerStatus = "idle"
	StatusRunning TimerStatus = "running"
	StatusPaused  TimerStatus = "paused"
)

type Talk struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Duration  int    `json:"durationSeconds"`
	Closing   int    `json:"closingSeconds"`
	Temporary bool   `json:"temporary,omitempty"`
	// CreatedAt is only set on temporary parts and ties the part to the
	// meeting session it was added in, so it can be purged when the next
	// congregation's meeting starts.
	CreatedAt time.Time `json:"-"`
}

type Config struct {
	DeviceName          string         `json:"deviceName"`
	AdvertisedBaseURL   string         `json:"advertisedBaseUrl"`
	ControlToken        string         `json:"controlToken"`
	MeetingType         string         `json:"meetingType"`
	MeetingStartTime    string         `json:"meetingStartTime"`
	MeetingStarts       []MeetingStart `json:"meetingStarts"`
	PrestartSeconds     int            `json:"prestartSeconds"`
	MidweekURL          string         `json:"midweekUrl"`
	AutoImportMidweek   bool           `json:"autoImportMidweek"`
	MidweekImportedWeek string         `json:"midweekImportedWeek,omitempty"`
	// CircuitOverseerExpiresAt is when the circuit-overseer-visit schedule stops
	// applying. The mode is "active" only while now < this time, so it scopes to
	// a single meeting session and auto-clears (see circuitOverseerDuration).
	// Persisted so it survives a reboot mid-visit and still expires on schedule.
	CircuitOverseerExpiresAt time.Time `json:"circuitOverseerExpiresAt,omitempty"`
	Schedule                 []Talk    `json:"schedule"`
}

type MeetingStart struct {
	ID           int    `json:"id"`
	Day          int    `json:"day"`
	Time         string `json:"time"`
	Congregation string `json:"congregation"`
}

type State struct {
	Status                   TimerStatus    `json:"status"`
	DeviceName               string         `json:"deviceName"`
	MeetingType              string         `json:"meetingType"`
	MeetingStartTime         string         `json:"meetingStartTime"`
	MeetingStarts            []MeetingStart `json:"meetingStarts"`
	PrestartLabel            string         `json:"prestartLabel"`
	PrestartSeconds          int            `json:"prestartSeconds"`
	PrestartActive           bool           `json:"prestartActive"`
	PrestartRemaining        int            `json:"prestartRemainingSeconds"`
	CurrentTalkID            int            `json:"currentTalkId"`
	CurrentTalkTitle         string         `json:"currentTalkTitle"`
	DurationSeconds          int            `json:"durationSeconds"`
	RemainingSeconds         int            `json:"remainingSeconds"`
	ElapsedSeconds           int            `json:"elapsedSeconds"`
	ClosingSeconds           int            `json:"closingSeconds"`
	OvertimeSeconds          int            `json:"overtimeSeconds"`
	CircuitOverseer          bool           `json:"circuitOverseer"`
	CircuitOverseerExpiresAt *time.Time     `json:"circuitOverseerExpiresAt,omitempty"`
	Schedule                 []Talk         `json:"schedule"`
	Now                      time.Time      `json:"now"`
	Bell                     int64          `json:"bell"`
	PairingActive            bool           `json:"pairingActive"`
	PairingExpiresAt         *time.Time     `json:"pairingExpiresAt,omitempty"`
}

type clockTime struct {
	hour   int
	minute int
}

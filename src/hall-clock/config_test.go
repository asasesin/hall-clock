package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The appliance once crash-looped on a Pi nobody could reach: an update
// restarted the app mid-write, os.WriteFile had already truncated config.json,
// and every subsequent start died on "unexpected end of JSON input". A clock
// that will not boot is worse than a clock that forgot its schedule.
func TestLoadConfigRecoversFromUnreadableFile(t *testing.T) {
	cases := []struct {
		name       string
		contents   string
		wantBackup bool
	}{
		{"empty (killed between truncate and write)", "", false},
		{"whitespace only", "\n\n", false},
		{"truncated mid-write", `{"deviceName":"Hall Clock","sched`, true},
		{"not json at all", "\x00\x00\x00\x00", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}

			config, err := loadConfig(path)
			if err != nil {
				t.Fatalf("a bad config must not stop the app from booting: %v", err)
			}
			if !config.AutoImportMidweek {
				t.Fatal("expected the defaults the app starts from")
			}

			// The bad file is kept for forensics, not silently destroyed.
			_, statErr := os.Stat(path + ".corrupt")
			if tc.wantBackup && statErr != nil {
				t.Fatalf("expected the unreadable config kept at %s.corrupt", path)
			}
			if !tc.wantBackup && statErr == nil {
				t.Fatal("an empty config needs no forensic copy")
			}
		})
	}
}

// The whole appliance boots through newServer, so prove the recovery reaches it
// rather than only the helper.
func TestNewServerBootsWithCorruptConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"deviceName":"Hall`), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := newServer(path)
	if err != nil {
		t.Fatalf("newServer must survive a corrupt config: %v", err)
	}
	if srv.config.ControlToken == "" {
		t.Fatal("expected a fresh control token; phones re-pair through /api/pairing")
	}
	// It rewrote a good config on the way up, so the next boot is clean.
	reloaded, err := loadConfig(path)
	if err != nil || reloaded.ControlToken != srv.config.ControlToken {
		t.Fatalf("expected a valid config written back, got %v (%v)", reloaded.ControlToken, err)
	}
}

func TestNewServerBootsWhenStartupConfigCannotBeSaved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
	})

	srv, err := newServer(path)
	if err != nil {
		t.Fatalf("newServer must boot even when startup config cannot be saved: %v", err)
	}
	if srv.config.ControlToken == "" {
		t.Fatal("expected an in-memory control token")
	}
	if len(srv.config.Schedule) == 0 {
		t.Fatal("expected default schedule in memory")
	}
}

// os.WriteFile truncates the target before writing. Any crash in that window
// leaves an empty config. Writing to a temp file and renaming means the real
// file is only ever replaced by a complete one.
func TestSaveConfigNeverLeavesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	good := Config{DeviceName: "Hall Clock", ControlToken: "token", PrestartSeconds: 300}
	if err := saveConfig(path, good); err != nil {
		t.Fatal(err)
	}

	// A save that fails must leave the previous config intact, not a stub.
	unserializable := Config{DeviceName: strings.Repeat("x", 8)}
	unserializable.Schedule = []Talk{{Title: "ok", Duration: 60}}
	if err := saveConfig(path, unserializable); err != nil {
		t.Fatal(err)
	}

	reloaded, err := loadConfig(path)
	if err != nil {
		t.Fatalf("config unreadable after save: %v", err)
	}
	if reloaded.DeviceName == "" {
		t.Fatal("expected a complete config on disk")
	}

	// No temp files left behind for the next boot to trip over.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config-") {
			t.Fatalf("staging file left behind: %s", entry.Name())
		}
	}
}

func TestSaveConfigWritesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfig(path, Config{ControlToken: "secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds the control token; CreateTemp makes 0600 but Chmod is what
	// guarantees it.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600 on a file holding the control token, got %v", perm)
	}
}

// A Twi workbook cached and served as "English": switching the weekend
// language moves MidweekLanguage but deliberately not the midweek URL, and
// load used to file that URL under the weekend's language blindly. The
// pre-load sweep then imported Twi items as "English", and the next switch to
// English put them on screen under the wrong label. Loading must drop the
// mismatched bookkeeping and, once no operator override holds, believe the
// URL over the label.
func TestLoadHealsMismatchedLanguageBookkeeping(t *testing.T) {
	twURL := "https://wol.jw.org/tw/wol/d/r33/lp-tw/202026245"
	esURL := "https://wol.jw.org/es/wol/d/r4/lp-s/202026245"
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	config := Config{
		Version:         currentConfigVersion,
		DeviceName:      "Hall Clock",
		MidweekURL:      twURL,
		MidweekLanguage: "en",
		MidweekLanguageSources: map[string]string{
			"en": twURL,
			"es": esURL,
		},
		MidweekLanguageSchedules: map[string]MidweekLanguageSchedule{
			"en": {ImportedWeek: "2026-W32", URL: twURL, Schedule: []Talk{{ID: 1, Title: "Nnianim Nsɛm", Duration: 60}}},
			"es": {ImportedWeek: "2026-W32", URL: esURL, Schedule: []Talk{{ID: 1, Title: "Palabras de introducción", Duration: 60}}},
		},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := newServerWithClock(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	if source, ok := srv.config.MidweekLanguageSources["en"]; ok {
		t.Fatalf("expected the Twi source filed under en to be dropped, got %s", source)
	}
	if _, ok := srv.config.MidweekLanguageSchedules["en"]; ok {
		t.Fatal("expected the Twi items cached as en to be dropped")
	}
	if srv.config.MidweekLanguageSources["es"] != esURL {
		t.Fatal("expected the correctly filed Spanish source to survive")
	}
	if _, ok := srv.config.MidweekLanguageSchedules["es"]; !ok {
		t.Fatal("expected the correctly filed Spanish cache to survive")
	}
	if srv.config.MidweekLanguage != "tw" {
		t.Fatalf("expected the label to follow the applied URL, got %q", srv.config.MidweekLanguage)
	}
	// The reconciled language's URL is now a trustworthy source.
	if srv.config.MidweekLanguageSources["tw"] != twURL {
		t.Fatalf("expected the active URL filed under tw, got %q", srv.config.MidweekLanguageSources["tw"])
	}
}

// Inside the override window the label may legitimately disagree with the URL
// — that is exactly what a weekend language switch looks like — so the label
// must survive a restart. The poisoned per-language entries still go.
func TestLoadKeepsOverriddenLanguageLabel(t *testing.T) {
	twURL := "https://wol.jw.org/tw/wol/d/r33/lp-tw/202026245"
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	config := Config{
		Version:                      currentConfigVersion,
		DeviceName:                   "Hall Clock",
		MidweekURL:                   twURL,
		MidweekLanguage:              "en",
		MidweekLanguageOverrideUntil: now.Add(time.Hour),
		MidweekLanguageSources:       map[string]string{"en": twURL},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := newServerWithClock(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	if srv.config.MidweekLanguage != "en" {
		t.Fatalf("expected the operator's choice to outlive a restart, got %q", srv.config.MidweekLanguage)
	}
	if _, ok := srv.config.MidweekLanguageSources["en"]; ok {
		t.Fatal("expected the Twi source filed under en to be dropped even during an override")
	}
}

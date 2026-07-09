package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPIN = "481902"

func newPairingTestServer(t *testing.T) (*server, *http.ServeMux) {
	t.Helper()
	srv, err := newServer(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	return srv, mux
}

// newSecuredTestServer returns a server with a PIN already set, i.e. the state
// a hall is in after setup — the bootstrap window shut.
func newSecuredTestServer(t *testing.T) (*server, *http.ServeMux) {
	t.Helper()
	srv, mux := newPairingTestServer(t)
	setPIN(t, srv, mux, testPIN)
	return srv, mux
}

func setPIN(t *testing.T, srv *server, mux *http.ServeMux, pin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/pin", strings.NewReader(`{"pin":"`+pin+`"}`))
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func claimPIN(mux *http.ServeMux, pin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/pairing/claim", strings.NewReader(`{"pin":"`+pin+`"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func pairingBody(t *testing.T, mux *http.ServeMux) map[string]any {
	t.Helper()
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/pairing", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing status, got %d", res.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The point of the rework: a LAN client can no longer read a token out of the
// pairing endpoint, which is how it used to bypass every protected route.
func TestPairingEndpointNeverLeaksControlToken(t *testing.T) {
	srv, mux := newSecuredTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pairing", nil)
	req.Host = "hallclock.local:8080"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected OK pairing response, got %d", res.Code)
	}
	if strings.Contains(res.Body.String(), srv.config.ControlToken) {
		t.Fatalf("pairing endpoint leaked the control token: %s", res.Body.String())
	}
}

// The PIN is a shared secret for the whole hall, so it must never come back out
// of the API — not in the config the setup page reads, and not in the state
// stream every subscriber gets.
func TestPINNeverLeavesTheServer(t *testing.T) {
	srv, mux := newSecuredTestServer(t)

	for _, path := range []string{"/api/config", "/api/state", "/api/pairing"} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		body := res.Body.String()
		for _, secret := range []string{testPIN, srv.config.ControlPINHash, srv.config.ControlPINSalt} {
			if secret != "" && strings.Contains(body, secret) {
				t.Fatalf("%s leaked PIN material: %s", path, body)
			}
		}
	}

	state, err := json.Marshal(srv.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), testPIN) || strings.Contains(string(state), srv.config.ControlPINHash) {
		t.Fatalf("broadcast state leaked PIN material: %s", state)
	}
}

// The config file is what somebody gets by pulling the SD card, so a readable
// PIN in it would make the whole scheme decorative.
func TestPINIsStoredHashedNotInTheClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	srv, err := newServer(path)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	if res := setPIN(t, srv, mux, testPIN); res.Code != http.StatusOK {
		t.Fatalf("expected the PIN to save, got %d: %s", res.Code, res.Body.String())
	}

	saved, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ControlPINHash == "" || saved.ControlPINSalt == "" {
		t.Fatal("expected a salt and hash to be persisted")
	}
	if saved.ControlPINHash == testPIN || saved.ControlPINSalt == testPIN {
		t.Fatal("expected the PIN to be hashed, not stored")
	}
	if !pinMatches(saved.ControlPINSalt, saved.ControlPINHash, testPIN) {
		t.Fatal("expected the persisted hash to verify the PIN")
	}
	if pinMatches(saved.ControlPINSalt, saved.ControlPINHash, "000000") {
		t.Fatal("expected a wrong PIN to be rejected")
	}
}

// Two halls picking the same PIN must not produce the same stored bytes.
func TestPINSaltIsPerInstall(t *testing.T) {
	saltA, hashA, err := newPINCredentials(testPIN)
	if err != nil {
		t.Fatal(err)
	}
	saltB, hashB, err := newPINCredentials(testPIN)
	if err != nil {
		t.Fatal(err)
	}
	if saltA == saltB || hashA == hashB {
		t.Fatal("expected a fresh salt (and so a different hash) per install")
	}
}

func TestPINPairsAndUnlocksProtectedRoutes(t *testing.T) {
	srv, mux := newSecuredTestServer(t)

	// A phone with no token is shut out.
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/control/start", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized before pairing, got %d", res.Code)
	}
	if got := claimPIN(mux, "000000"); got.Code != http.StatusUnauthorized {
		t.Fatalf("expected a wrong PIN to be refused, got %d", got.Code)
	}

	claim := claimPIN(mux, testPIN)
	if claim.Code != http.StatusOK {
		t.Fatalf("expected the PIN to pair, got %d: %s", claim.Code, claim.Body.String())
	}
	var paired struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(claim.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}
	if paired.Token != srv.config.ControlToken {
		t.Fatalf("expected the control token back, got %q", paired.Token)
	}

	start := httptest.NewRequest(http.MethodPost, "/api/control/start", nil)
	start.Header.Set("X-Wall-Clock-Token", paired.Token)
	startRes := httptest.NewRecorder()
	mux.ServeHTTP(startRes, start)
	if startRes.Code != http.StatusOK {
		t.Fatalf("expected the paired token to work, got %d: %s", startRes.Code, startRes.Body.String())
	}
}

// A long-lived PIN has to survive somebody grinding at it, so wrong guesses buy
// a lockout rather than another turn.
func TestWrongPINsLockOutGuessing(t *testing.T) {
	srv, mux := newSecuredTestServer(t)
	now := time.Now()
	srv.clock = func() time.Time { return now }

	for i := 1; i < maxPINFailures; i++ {
		if res := claimPIN(mux, "000000"); res.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, res.Code)
		}
	}
	if res := claimPIN(mux, "000000"); res.Code != http.StatusUnauthorized {
		t.Fatalf("expected the last wrong attempt to be a 401, got %d", res.Code)
	}
	// Locked now: even the right PIN waits.
	if res := claimPIN(mux, testPIN); res.Code != http.StatusTooManyRequests {
		t.Fatalf("expected a lockout, got %d: %s", res.Code, res.Body.String())
	}

	now = now.Add(pinLockout + time.Second)
	if res := claimPIN(mux, testPIN); res.Code != http.StatusOK {
		t.Fatalf("expected pairing to work once the lockout lapsed, got %d", res.Code)
	}
}

// Guessing in parallel must not buy more attempts than guessing in series.
// Counting a failure only after the (deliberately slow) derivation finished let
// a whole burst read an untripped counter and slip through together, turning a
// five-per-lockout cap into as many tries as the caller could open sockets.
func TestConcurrentWrongPINsCannotOutrunTheLockout(t *testing.T) {
	_, mux := newSecuredTestServer(t)

	const burst = 40
	codes := make(chan int, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- claimPIN(mux, "000000").Code
		}()
	}
	wg.Wait()
	close(codes)

	tried := 0
	for code := range codes {
		if code == http.StatusUnauthorized {
			tried++
		}
	}
	if tried > maxPINFailures {
		t.Fatalf("a burst of %d got %d guesses past a %d-attempt cap", burst, tried, maxPINFailures)
	}
	// And the lockout really did engage, rather than the burst simply failing.
	if res := claimPIN(mux, testPIN); res.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the burst to trip the lockout, got %d", res.Code)
	}
}

func TestSuccessfulPairingClearsFailureCount(t *testing.T) {
	_, mux := newSecuredTestServer(t)

	for i := 0; i < maxPINFailures-1; i++ {
		claimPIN(mux, "000000")
	}
	if res := claimPIN(mux, testPIN); res.Code != http.StatusOK {
		t.Fatalf("expected the right PIN to pair, got %d", res.Code)
	}
	// The counter reset, so a fresh run of wrong guesses gets the full budget
	// rather than tripping the lockout immediately.
	for i := 1; i < maxPINFailures; i++ {
		if res := claimPIN(mux, "000000"); res.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d after a success: expected 401, got %d", i, res.Code)
		}
	}
}

// First boot with no PIN has to let somebody in, or the appliance could never
// be set up without SSH.
func TestFirstBootGraceWindowPairsWithoutAPIN(t *testing.T) {
	_, mux := newPairingTestServer(t)

	status := pairingBody(t, mux)
	if status["pairingOpen"] != true || status["pinConfigured"] != false {
		t.Fatalf("expected an open bootstrap window with no PIN set, got %v", status)
	}
	if res := claimPIN(mux, ""); res.Code != http.StatusOK {
		t.Fatalf("expected the grace window to pair without a PIN, got %d: %s", res.Code, res.Body.String())
	}
}

func TestGraceWindowExpires(t *testing.T) {
	srv, mux := newPairingTestServer(t)
	now := time.Now()
	srv.clock = func() time.Time { return now }

	now = now.Add(pairingGrace + time.Second)
	if res := claimPIN(mux, ""); res.Code != http.StatusForbidden {
		t.Fatalf("expected the lapsed grace window to refuse, got %d: %s", res.Code, res.Body.String())
	}
}

// Setting a PIN is the moment the appliance becomes secured, so the wide-open
// bootstrap window must not outlive it.
func TestSettingAPINClosesTheGraceWindow(t *testing.T) {
	srv, mux := newPairingTestServer(t)
	if !srv.pairingOpenLocked(srv.clock()) {
		t.Fatal("expected the bootstrap window to start open")
	}

	if res := setPIN(t, srv, mux, testPIN); res.Code != http.StatusOK {
		t.Fatalf("expected the PIN to save, got %d", res.Code)
	}
	if srv.pairingOpenLocked(srv.clock()) {
		t.Fatal("expected setting a PIN to close the bootstrap window")
	}
	if res := claimPIN(mux, ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("expected an empty PIN to be refused once one is set, got %d", res.Code)
	}
}

// A restart with a PIN configured must not reopen the door.
func TestGraceWindowDoesNotReopenOnceAPINIsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	srv, err := newServer(path)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes("")
	if err != nil {
		t.Fatal(err)
	}
	if res := setPIN(t, srv, mux, testPIN); res.Code != http.StatusOK {
		t.Fatalf("expected the PIN to save, got %d", res.Code)
	}

	restarted, err := newServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.pairingOpenLocked(time.Now()) {
		t.Fatal("expected a restart with a PIN set to come up closed")
	}
}

// Adding a second phone should not require telling anyone the PIN.
func TestPairedControllerCanOpenAWindowForAnotherPhone(t *testing.T) {
	srv, mux := newSecuredTestServer(t)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/pairing/enable", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without a token, got %d", res.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/pairing/enable", nil)
	req.Header.Set("X-Wall-Clock-Token", srv.config.ControlToken)
	authed := httptest.NewRecorder()
	mux.ServeHTTP(authed, req)
	if authed.Code != http.StatusOK {
		t.Fatalf("expected a paired controller to open pairing, got %d", authed.Code)
	}

	if res := claimPIN(mux, ""); res.Code != http.StatusOK {
		t.Fatalf("expected the open window to pair without a PIN, got %d", res.Code)
	}
	// One window, one phone.
	if res := claimPIN(mux, ""); res.Code != http.StatusUnauthorized {
		t.Fatalf("expected the window to close after a phone joined, got %d", res.Code)
	}
}

func TestSetPINRejectsWeakInput(t *testing.T) {
	srv, mux := newSecuredTestServer(t)

	for _, pin := range []string{"", "1", "abc", strings.Repeat("9", maxPINLength+1)} {
		if res := setPIN(t, srv, mux, pin); res.Code != http.StatusBadRequest {
			t.Fatalf("expected %q to be refused, got %d", pin, res.Code)
		}
	}
	// The old PIN still works after a rejected change.
	if res := claimPIN(mux, testPIN); res.Code != http.StatusOK {
		t.Fatalf("expected the existing PIN to survive a rejected change, got %d", res.Code)
	}
}

func TestSetPINRequiresAnExistingToken(t *testing.T) {
	_, mux := newSecuredTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/pairing/pin", strings.NewReader(`{"pin":"999999"}`))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without a token, got %d", res.Code)
	}
	if got := claimPIN(mux, "999999"); got.Code != http.StatusUnauthorized {
		t.Fatalf("expected the unauthorized PIN change to have no effect, got %d", got.Code)
	}
}

// Changing the PIN must not evict the phones already in use — an operator
// mid-meeting should never be logged out by somebody tidying up settings.
func TestChangingThePINKeepsPairedPhonesWorking(t *testing.T) {
	srv, mux := newSecuredTestServer(t)
	token := srv.config.ControlToken

	if res := setPIN(t, srv, mux, "775533"); res.Code != http.StatusOK {
		t.Fatalf("expected the PIN change to save, got %d", res.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/control/start", nil)
	req.Header.Set("X-Wall-Clock-Token", token)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected an already-paired phone to keep working, got %d", res.Code)
	}
	if got := claimPIN(mux, "775533"); got.Code != http.StatusOK {
		t.Fatalf("expected the new PIN to pair, got %d", got.Code)
	}
	if got := claimPIN(mux, testPIN); got.Code != http.StatusUnauthorized {
		t.Fatalf("expected the old PIN to stop working, got %d", got.Code)
	}
}

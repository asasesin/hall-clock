(function () {
  const TOKEN_KEY = "wallClockControlToken";

  function getToken() {
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token");
    if (token) {
      localStorage.setItem(TOKEN_KEY, token);
      params.delete("token");
      const query = params.toString();
      const clean = window.location.pathname + (query ? `?${query}` : "");
      window.history.replaceState({}, "", clean);
    }
    return localStorage.getItem(TOKEN_KEY) || "";
  }

  // A request that never settles leaves its caller's "pending" flag set forever,
  // which is how a dropped wifi link used to permanently disable the Start
  // button. Always give fetch a deadline. Imports and updates talk to the
  // network and are legitimately slow, so the default is generous; the control
  // buttons pass something short because a person is waiting on them.
  const DEFAULT_TIMEOUT_MS = 30000;

  async function postJSON(path, body, options) {
    const timeoutMs = (options && options.timeoutMs) || DEFAULT_TIMEOUT_MS;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    let response;
    try {
      response = await fetch(path, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Wall-Clock-Token": getToken(),
        },
        body: JSON.stringify(body || {}),
        signal: controller.signal,
      });
    } catch (cause) {
      const error = new Error(cause && cause.name === "AbortError" ? `${path} timed out` : String(cause));
      error.timedOut = cause && cause.name === "AbortError";
      throw error;
    } finally {
      clearTimeout(timer);
    }
    if (!response.ok) {
      // A 401 means this browser's token is dead — usually the config was reset
      // and the server minted a new one. Drop it so the next ensurePaired()
      // pairs again instead of retrying a credential that can never work.
      if (response.status === 401 && path !== "/api/pairing/claim") {
        clearToken();
      }
      // Carry the status: callers must tell "your token is bad" apart from an
      // ordinary refusal like advancing past the last part of the meeting.
      const error = new Error(await response.text());
      error.status = response.status;
      throw error;
    }
    return response.json();
  }

  // Pairing asks for the hall's PIN, which the congregation sets in /setup.
  // Nothing about pairing ever touches the display: the TV shows the clock and
  // only the clock. Before prompting, check whether a no-PIN window is open —
  // the first-boot grace period, or "add a phone" started from a paired
  // controller — and if so pair silently.

  function clearToken() {
    localStorage.removeItem(TOKEN_KEY);
  }

  // tokenWorks asks the server whether the stored token is still valid. Trusting
  // localStorage blindly is what turned a config reset into a browser that
  // silently 401s every write and never re-pairs.
  async function tokenWorks(token) {
    if (!token) return false;
    try {
      const response = await fetch("/api/pairing/verify", {
        headers: { "X-Wall-Clock-Token": token },
      });
      return response.ok;
    } catch (error) {
      // Offline is not the same as unpaired: keep the token and let the page
      // retry rather than dumping the operator into a PIN prompt mid-meeting.
      console.error(error);
      return true;
    }
  }

  async function pairingStatus() {
    const response = await fetch("/api/pairing");
    if (!response.ok) throw new Error("pairing status unavailable");
    return response.json();
  }

  async function claimPairing(pin) {
    const result = await postJSON("/api/pairing/claim", { pin: pin || "" }, { timeoutMs: 15000 });
    const token = (result && result.token) || "";
    if (token) {
      localStorage.setItem(TOKEN_KEY, token);
    }
    return token;
  }

  // A clock with no PIN set and no window open cannot be paired by anybody:
  // there is no PIN to type. Asking for one is a dead end that also hides the
  // settings page behind a modal, so say what actually unblocks it instead.
  function buildNoPinPrompt() {
    const backdrop = document.createElement("div");
    backdrop.className = "pairing-backdrop";
    backdrop.innerHTML = `
      <section class="pairing-card" role="dialog" aria-modal="true" aria-labelledby="pairingTitle">
        <p class="eyebrow">Controller pairing</p>
        <h2 id="pairingTitle">Pairing is closed</h2>
        <p class="pairing-lead">
          No PIN has been set on this clock, and the window for setting one has
          closed. Restart the hall clock to reopen it for 15 minutes, then set a
          PIN from this page.
        </p>
        <div class="pairing-actions">
          <button type="button" class="action action-primary pairing-recheck">Check again</button>
        </div>
      </section>`;
    return backdrop;
  }

  function showNoPinPrompt() {
    return new Promise((resolve) => {
      const backdrop = buildNoPinPrompt();
      document.body.appendChild(backdrop);
      backdrop.querySelector(".pairing-recheck").addEventListener("click", async () => {
        backdrop.remove();
        resolve(await ensurePaired());
      });
    });
  }

  function buildPinPrompt() {
    const backdrop = document.createElement("div");
    backdrop.className = "pairing-backdrop";
    backdrop.innerHTML = `
      <section class="pairing-card" role="dialog" aria-modal="true" aria-labelledby="pairingTitle">
        <p class="eyebrow">Controller pairing</p>
        <h2 id="pairingTitle">Enter the hall PIN</h2>
        <p class="notice pairing-error hidden" role="alert"></p>
        <input class="pairing-input" type="password" inputmode="numeric" autocomplete="current-password"
               maxlength="32" placeholder="••••" aria-label="Hall PIN">
        <div class="pairing-actions">
          <button type="button" class="action action-primary pairing-submit">Pair this device</button>
        </div>
      </section>`;
    return backdrop;
  }

  // showPinPrompt resolves with a token once the operator enters the PIN. It
  // never rejects: a page that cannot pair should sit on the prompt rather than
  // fall through to a controller whose every button would fail with a 401.
  function showPinPrompt() {
    return new Promise((resolve) => {
      const backdrop = buildPinPrompt();
      const input = backdrop.querySelector(".pairing-input");
      const submit = backdrop.querySelector(".pairing-submit");
      const errorEl = backdrop.querySelector(".pairing-error");
      document.body.appendChild(backdrop);
      input.focus();

      function showError(message) {
        errorEl.textContent = message;
        errorEl.classList.toggle("hidden", !message);
      }

      async function attempt() {
        if (!input.value) {
          showError("Type the PIN first.");
          return;
        }
        submit.disabled = true;
        try {
          const token = await claimPairing(input.value);
          if (!token) throw new Error("no token returned");
          backdrop.remove();
          resolve(token);
          return;
        } catch (error) {
          input.value = "";
          if (error.status === 429) {
            // Repeated wrong PINs lock guessing for a few minutes.
            showError("Too many wrong tries. Wait five minutes, then try again.");
          } else if (error.status === 401) {
            showError("That PIN was not right.");
          } else if (error.status === 403) {
            showError("No PIN has been set on this clock yet. Set one in Setup from a phone that is already paired.");
          } else {
            console.error(error);
            showError("Could not reach the clock. Check the wifi and try again.");
          }
        } finally {
          submit.disabled = false;
          input.focus();
        }
      }

      input.addEventListener("keydown", (event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          attempt();
        }
      });
      submit.addEventListener("click", attempt);
    });
  }

  // ensurePaired resolves with a control token, prompting for the hall PIN when
  // this browser has none and no open window can pair it silently.
  async function ensurePaired() {
    const existing = getToken();
    if (existing && (await tokenWorks(existing))) {
      return existing;
    }
    if (existing) {
      clearToken();
    }
    try {
      const status = await pairingStatus();
      if (status.pairingOpen) {
        const token = await claimPairing("");
        if (token) return token;
      }
      if (!status.pinConfigured) {
        return showNoPinPrompt();
      }
    } catch (error) {
      // Fall through to the prompt: an unreachable status endpoint should not
      // stop somebody who knows the PIN from typing it.
      console.error(error);
    }
    return showPinPrompt();
  }

  // showPairingPIN reads the PIN in force. Needs a token, so only an
  // already-paired controller can see it.
  async function showPairingPIN() {
    const response = await fetch("/api/pairing/pin", {
      headers: { "X-Wall-Clock-Token": getToken() },
    });
    if (!response.ok) throw new Error("could not read the PIN");
    return response.json();
  }

  // setPairingPIN changes the hall PIN. Setup calls this; it needs an existing
  // token, so only an already-paired controller can rotate it.
  async function setPairingPIN(pin) {
    return postJSON("/api/pairing/pin", { pin }, { timeoutMs: 15000 });
  }

  function subscribe(onState, onConnection) {
    const source = new EventSource("/events");
    source.addEventListener("open", () => onConnection && onConnection(true));
    source.addEventListener("error", () => onConnection && onConnection(false));
    source.addEventListener("state", (event) => onState(JSON.parse(event.data)));
    return source;
  }

  function formatTime(seconds) {
    const negative = seconds < 0;
    const total = Math.abs(seconds);
    const minutes = Math.floor(total / 60);
    const secs = total % 60;
    return `${negative ? "+" : ""}${String(minutes).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  }

  function formatClock(isoDate) {
    const date = isoDate ? new Date(isoDate) : new Date();
    return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }

  function formatStartTime(value) {
    const match = /^(\d{1,2}):(\d{2})$/.exec(String(value || ""));
    if (!match) return value || "";
    const date = new Date();
    date.setHours(Number(match[1]), Number(match[2]), 0, 0);
    return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  }

  function statusLabel(status) {
    if (status === "running") return "Running";
    if (status === "paused") return "Paused";
    return "Idle";
  }


  window.WallClock = {
    getToken,
    clearToken,
    ensurePaired,
    pairingStatus,
    showPairingPIN,
    setPairingPIN,
    postJSON,
    subscribe,
    formatTime,
    formatClock,
    formatStartTime,
    statusLabel,
  };
})();

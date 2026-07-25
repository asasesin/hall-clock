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

  function buildPinPrompt() {
    const backdrop = document.createElement("div");
    backdrop.className = "pairing-backdrop";
    backdrop.innerHTML = `
      <section class="pairing-card" role="dialog" aria-modal="true" aria-labelledby="pairingTitle">
        <p class="eyebrow">Controller pairing</p>
        <h2 id="pairingTitle">Enter the hall PIN</h2>
        <p class="pairing-lead">This phone needs the PIN once. Ask whoever looks after the clock if you do not have it.</p>
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
          showError("Enter the PIN.");
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
            showError("Too many wrong PINs. Wait a few minutes, then try again.");
          } else if (error.status === 401) {
            showError("Wrong PIN.");
          } else if (error.status === 403) {
            showError("Pairing is closed on this clock. Ask for a PIN to be set in Setup.");
          } else {
            console.error(error);
            showError("Could not pair. Check the connection and try again.");
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
    if (existing) {
      return existing;
    }
    try {
      const status = await pairingStatus();
      if (status.pairingOpen) {
        const token = await claimPairing("");
        if (token) return token;
      }
    } catch (error) {
      // Fall through to the prompt: an unreachable status endpoint should not
      // stop somebody who knows the PIN from typing it.
      console.error(error);
    }
    return showPinPrompt();
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

  let bellContext = null;

  function playBell() {
    const AudioContext = window.AudioContext || window.webkitAudioContext;
    if (!AudioContext) return;
    if (!bellContext) {
      bellContext = new AudioContext();
    }
    if (bellContext.state === "suspended") {
      bellContext.resume();
    }
    const context = bellContext;
    const gain = context.createGain();
    gain.gain.setValueAtTime(0.0001, context.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.35, context.currentTime + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, context.currentTime + 1.4);
    gain.connect(context.destination);

    const oscillator = context.createOscillator();
    oscillator.type = "sine";
    oscillator.frequency.setValueAtTime(880, context.currentTime);
    oscillator.connect(gain);
    oscillator.start();
    oscillator.stop(context.currentTime + 1.45);
  }

  window.WallClock = {
    getToken,
    ensurePaired,
    pairingStatus,
    setPairingPIN,
    postJSON,
    subscribe,
    formatTime,
    formatClock,
    formatStartTime,
    statusLabel,
    playBell,
  };
})();

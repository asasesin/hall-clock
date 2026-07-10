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

  // ensureToken returns a control token, auto-pairing from the open
  // /api/pairing endpoint when this browser doesn't have one yet. This lets a
  // printed QR point at a plain, tokenless URL (e.g. http://hallclock.local/
  // control) and have the page pair itself on first load. Pairing is
  // intentionally open on the LAN, so this grants no access the network did
  // not already allow.
  async function ensureToken() {
    const existing = getToken();
    if (existing) {
      return existing;
    }
    try {
      const response = await fetch("/api/pairing");
      if (!response.ok) {
        return "";
      }
      const pairing = await response.json();
      const url = new URL(pairing.controlUrl, window.location.href);
      const token = url.searchParams.get("token") || "";
      if (token) {
        localStorage.setItem(TOKEN_KEY, token);
      }
      return token;
    } catch (error) {
      console.error(error);
      return "";
    }
  }

  async function postJSON(path, body) {
    const response = await fetch(path, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Wall-Clock-Token": getToken(),
      },
      body: JSON.stringify(body || {}),
    });
    if (!response.ok) {
      // Carry the status: callers must tell "your token is bad" apart from an
      // ordinary refusal like advancing past the last part of the meeting.
      const error = new Error(await response.text());
      error.status = response.status;
      throw error;
    }
    return response.json();
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
    ensureToken,
    postJSON,
    subscribe,
    formatTime,
    formatClock,
    formatStartTime,
    statusLabel,
    playBell,
  };
})();

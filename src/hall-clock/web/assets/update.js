(function () {
  const card = document.getElementById("updateCard");
  const versionPill = document.getElementById("updateVersion");
  const note = document.getElementById("updateNote");
  const button = document.getElementById("updateBtn");
  const checkButton = document.getElementById("updateCheckBtn");
  const status = document.getElementById("updateStatus");

  // Phases the updater script reports through its status file.
  const IN_FLIGHT = ["checking", "downloading", "restarting"];
  const POLL_MS = 2000;
  const IDLE_POLL_MS = 10000;

  let pollTimer = null;
  let updating = false;
  // clearTimeout cancels a queued poll but not one already awaiting its fetch.
  // Every action bumps this, and a tick whose generation is stale throws its
  // result away instead of rendering over newer state.
  let generation = 0;

  async function fetchInfo(refresh) {
    const response = await fetch(`/api/update${refresh ? "?refresh=1" : ""}`);
    if (!response.ok) throw new Error(await response.text());
    return response.json();
  }

  function phaseMessage(info) {
    const phase = info.status?.phase;
    if (info.pending && !phase) return "Requested...";
    switch (phase) {
      case "checking":
        return "Checking...";
      case "downloading":
        return info.status.message || "Downloading...";
      case "restarting":
        return "Restarting...";
      case "updated":
        return info.status.message || "Updated";
      case "failed":
        return info.status.message || "Update failed";
      case "deferred":
        return info.status.message || "Deferred";
      default:
        return "";
    }
  }

  function lastChecked(info) {
    if (!info.status?.at) return "";
    const at = new Date(info.status.at);
    if (Number.isNaN(at.getTime())) return "";
    return `Last checked ${WallClock.formatClock(info.status.at)}`;
  }

  function render(info) {
    card.classList.toggle("hidden", !info.supported);
    if (!info.supported) return false;

    versionPill.textContent = `Running ${info.version}`;

    const phase = info.status?.phase;
    const inFlight = info.pending || IN_FLIGHT.includes(phase);
    updating = inFlight;

    if (info.checkError) {
      note.textContent = info.checkError;
    } else if (info.updateAvailable) {
      note.textContent = `${info.latest} available`;
    } else if (info.version === "dev") {
      note.textContent = "Development build";
    } else {
      note.textContent = lastChecked(info) || "Up to date";
    }

    status.textContent = phaseMessage(info);
    status.classList.toggle("error", phase === "failed");

    button.textContent = info.updateAvailable ? `Update to ${info.latest}` : "Up to date";
    button.disabled = inFlight || !info.updateAvailable || !info.canUpdate;
    checkButton.disabled = inFlight;

    // The Update button restarts the app, which resets a running countdown, so
    // it stays disabled until the timer is back to idle (same rule as CO mode).
    if (info.updateAvailable && !info.canUpdate) {
      status.textContent = "Reset the timer to idle to update";
    }
    return true;
  }

  function schedule(delay) {
    clearTimeout(pollTimer);
    pollTimer = setTimeout(() => tick(false), delay);
  }

  async function tick(refresh) {
    // Cancel any poll already queued, so a click never runs alongside one and
    // re-renders stale state over the result.
    clearTimeout(pollTimer);
    const mine = ++generation;
    try {
      const info = await fetchInfo(refresh);
      if (mine !== generation) return;
      // Nothing to poll for on a box without the updater wired up: the card is
      // hidden and the state cannot change.
      if (!render(info)) return;
      schedule(updating ? POLL_MS : IDLE_POLL_MS);
    } catch (error) {
      if (mine !== generation) return;
      // A successful update restarts the app, so the poll that spans the
      // restart fails. Keep polling: the page recovers on its own once the new
      // binary answers, and shows "Updated to vX" from the status file.
      status.textContent = updating ? "Restarting..." : "";
      schedule(POLL_MS);
    }
  }

  button.addEventListener("click", async () => {
    clearTimeout(pollTimer);
    // Retire any poll already awaiting a response: its stale `pending:false`
    // would re-enable the button and let a second update be requested.
    generation++;
    button.disabled = true;
    status.classList.remove("error");
    status.textContent = "Starting update...";
    try {
      await WallClock.postJSON("/api/update", {});
      updating = true;
      schedule(POLL_MS);
    } catch (error) {
      // The server refused (not idle, or already updating). Re-render from a
      // fresh fetch rather than leaving the button disabled until the next poll.
      const message = String(error.message || error).trim() || "Could not start the update";
      await tick(false);
      status.textContent = message;
      status.classList.add("error");
    }
  });

  checkButton.addEventListener("click", () => {
    tick(true).catch(() => {});
  });

  tick(false);
})();

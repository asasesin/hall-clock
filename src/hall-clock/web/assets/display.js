(function () {
  const timerPanel = document.getElementById("timerPanel");
  const timeValue = document.getElementById("timeValue");
  const progressFill = document.getElementById("progressFill");
  const statusFooter = document.getElementById("statusFooter");
  const clockValue = document.getElementById("clockValue");
  const connection = document.getElementById("connection");
  let lastBell = -1;

  function render(state) {
    const prestart = state.prestartActive;
    const clockMode = !prestart && state.status === "idle";
    document.body.classList.toggle("clock-mode", clockMode);
    timeValue.textContent = clockMode
      ? WallClock.formatClock(state.now)
      : WallClock.formatTime(prestart ? state.prestartRemainingSeconds : state.remainingSeconds);
    statusFooter.textContent = prestart
      ? `${state.prestartLabel ? `${state.prestartLabel} — starts` : "Starts"} at ${state.meetingStartTime}`
      : "";
    clockValue.textContent = WallClock.formatClock(state.now);

    const duration = Math.max(1, prestart ? state.prestartSeconds : state.durationSeconds);
    const remaining = Math.max(0, prestart ? state.prestartRemainingSeconds : state.remainingSeconds);
    const percent = Math.max(0, Math.min(100, (remaining / duration) * 100));
    progressFill.style.width = `${percent}%`;

    const timing = state.status === "running" || state.status === "paused";
    timerPanel.classList.toggle("warning", timing && !prestart && !clockMode && state.remainingSeconds <= state.closingSeconds && state.remainingSeconds >= 0);
    timerPanel.classList.toggle("overtime", timing && !prestart && !clockMode && state.remainingSeconds < 0);

    if (state.bell !== lastBell) {
      const firstState = lastBell === -1;
      lastBell = state.bell;
      if (!firstState) {
        WallClock.playBell();
      }
    }
  }

  WallClock.subscribe(render, (online) => {
    connection.textContent = online ? "Online" : "Offline";
    connection.classList.toggle("online", online);
    connection.classList.toggle("offline", !online);
    connection.classList.toggle("hidden", online);
  });
})();

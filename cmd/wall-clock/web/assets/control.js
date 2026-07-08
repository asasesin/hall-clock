(function () {
  const tokenWarning = document.getElementById("tokenWarning");
  const meetingType = document.getElementById("meetingType");
  const talkTitle = document.getElementById("talkTitle");
  const timeValue = document.getElementById("timeValue");
  const talkSelect = document.getElementById("talkSelect");
  const startBtn = document.getElementById("startBtn");
  const pauseBtn = document.getElementById("pauseBtn");
  const resetBtn = document.getElementById("resetBtn");
  const partPosition = document.getElementById("partPosition");
  const nextPart = document.getElementById("nextPart");
  let lastBell = -1;
  let scheduleKey = "";
  let resetArmTimeout = null;

  function render(state) {
    meetingType.textContent = state.meetingType === "weekend" ? "Weekend meeting" : "Midweek meeting";
    const prestart = state.prestartActive;
    talkTitle.textContent = prestart ? (state.prestartLabel || "Meeting starts soon") : state.currentTalkTitle || "Ready";
    timeValue.textContent = WallClock.formatTime(prestart ? state.prestartRemainingSeconds : state.remainingSeconds);

    const timing = state.status === "running" || state.status === "paused";
    timeValue.classList.toggle("warning", timing && !prestart && state.remainingSeconds <= state.closingSeconds && state.remainingSeconds >= 0);
    timeValue.classList.toggle("overtime", timing && !prestart && state.remainingSeconds < 0);
    startBtn.disabled = state.status === "running";
    startBtn.textContent = state.status === "paused" ? "Resume" : "Start";
    pauseBtn.disabled = state.status !== "running";
    resetBtn.disabled = state.status === "idle" && state.remainingSeconds === state.durationSeconds;
    if (resetBtn.disabled && resetBtn.classList.contains("armed")) {
      disarmReset();
    }

    const schedule = state.schedule || [];
    const index = schedule.findIndex((talk) => talk.id === state.currentTalkId);
    partPosition.textContent = index >= 0 ? `Part ${index + 1} of ${schedule.length}` : "Schedule";
    const next = index >= 0 ? schedule[index + 1] : undefined;
    nextPart.textContent = next ? `Next: ${next.title}` : "Last part of the meeting";
    renderTalkPicker(schedule, state.currentTalkId);

    if (state.bell !== lastBell) {
      const firstState = lastBell === -1;
      lastBell = state.bell;
      if (!firstState) {
        WallClock.playBell();
      }
    }
  }

  async function command(path, body) {
    try {
      await WallClock.postJSON(path, body);
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      console.error(error);
    }
  }

  function renderTalkPicker(schedule, currentId) {
    const key = JSON.stringify(schedule.map((talk) => [talk.id, talk.title, talk.durationSeconds]));
    if (key !== scheduleKey) {
      scheduleKey = key;
      talkSelect.innerHTML = "";
      schedule.forEach((talk, index) => {
        const option = document.createElement("option");
        option.value = String(talk.id);
        option.textContent = `${index + 1}. ${talk.title} — ${Math.round(talk.durationSeconds / 60)} min`;
        talkSelect.appendChild(option);
      });
    }
    if (document.activeElement !== talkSelect && talkSelect.value !== String(currentId)) {
      talkSelect.value = String(currentId);
    }
  }

  if (!WallClock.getToken()) {
    tokenWarning.classList.remove("hidden");
  }

  function disarmReset() {
    clearTimeout(resetArmTimeout);
    resetArmTimeout = null;
    resetBtn.classList.remove("armed");
    resetBtn.textContent = "Reset";
  }

  startBtn.addEventListener("click", () => command("/api/control/start"));
  pauseBtn.addEventListener("click", () => command("/api/control/pause"));
  document.getElementById("prevBtn").addEventListener("click", () => command("/api/control/previous"));
  document.getElementById("nextBtn").addEventListener("click", () => command("/api/control/next"));
  talkSelect.addEventListener("change", () => command("/api/control/select", { talkId: Number(talkSelect.value) }));
  resetBtn.addEventListener("click", () => {
    if (!resetBtn.classList.contains("armed")) {
      resetBtn.classList.add("armed");
      resetBtn.textContent = "Tap to confirm";
      resetArmTimeout = setTimeout(disarmReset, 3000);
      return;
    }
    disarmReset();
    command("/api/control/reset");
  });
  document.getElementById("bellBtn").addEventListener("click", () => command("/api/control/bell"));
  document.querySelectorAll("[data-adjust]").forEach((button) => {
    button.addEventListener("click", () => {
      command("/api/control/adjust", { deltaSeconds: Number(button.dataset.adjust) });
    });
  });

  WallClock.subscribe(render);
})();

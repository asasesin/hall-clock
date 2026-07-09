(function () {
  const tokenWarning = document.getElementById("tokenWarning");
  const meetingType = document.getElementById("meetingType");
  const timeValue = document.getElementById("timeValue");
  const partPickerBtn = document.getElementById("partPickerBtn");
  const partPickerList = document.getElementById("partPickerList");
  const adhocPartBtn = document.getElementById("adhocPartBtn");
  const adhocPartPanel = document.getElementById("adhocPartPanel");
  const adhocPartTitleInput = document.getElementById("adhocPartTitleInput");
  const adhocPartMinutesInput = document.getElementById("adhocPartMinutesInput");
  const cancelAdhocPartBtn = document.getElementById("cancelAdhocPartBtn");
  const currentPartTitle = document.getElementById("currentPartTitle");
  const currentPartDuration = document.getElementById("currentPartDuration");
  const startBtn = document.getElementById("startBtn");
  const resetBtn = document.getElementById("resetBtn");
  const partPosition = document.getElementById("partPosition");
  const nextPart = document.getElementById("nextPart");
  const statusBadge = document.getElementById("statusBadge");
  const coToggle = document.getElementById("coToggle");
  const coHint = document.getElementById("coHint");
  let lastBell = -1;
  let scheduleKey = "";
  let resetArmTimeout = null;
  let partArmTimeout = null;
  let latestStatus = "idle";
  let latestState = null;
  let timerCommandPending = false;

  function render(state) {
    latestState = state;
    latestStatus = state.status;
    document.title = `${state.deviceName || "Hall Clock"} Control`;
    const meetingLabel = state.meetingType === "weekend" ? "Weekend meeting" : "Midweek meeting";
    meetingType.textContent = state.circuitOverseer ? `${meetingLabel} · CO visit` : meetingLabel;
    meetingType.classList.toggle("co-active", Boolean(state.circuitOverseer));
    const prestart = state.prestartActive;
    timeValue.textContent = WallClock.formatTime(prestart ? state.prestartRemainingSeconds : state.remainingSeconds);

    const timing = state.status === "running" || state.status === "paused";
    timeValue.classList.toggle("warning", timing && !prestart && state.remainingSeconds <= state.closingSeconds && state.remainingSeconds >= 0);
    timeValue.classList.toggle("overtime", timing && !prestart && state.remainingSeconds < 0);
    startBtn.disabled = timerCommandPending;
    startBtn.dataset.status = state.status;
    if (!timerCommandPending) {
      startBtn.textContent = state.status === "running" ? "Pause" : state.status === "paused" ? "Resume" : "Start";
    }
    resetBtn.disabled = state.status === "idle" && state.remainingSeconds === state.durationSeconds;
    if (resetBtn.disabled && resetBtn.classList.contains("armed")) {
      disarmReset();
    }
    if (state.status === "idle") {
      disarmPartButtons();
    }
    statusBadge.textContent = prestart ? "Countdown" : WallClock.statusLabel(state.status);
    statusBadge.classList.toggle("running", state.status === "running");
    statusBadge.classList.toggle("paused", state.status === "paused");
    statusBadge.classList.toggle("prestart", prestart);
    coToggle.setAttribute("aria-checked", state.circuitOverseer ? "true" : "false");
    // CO mode reshapes the schedule, so it's editable only while idle.
    coToggle.disabled = state.status !== "idle";
    coToggle.title = coToggle.disabled
      ? "Circuit overseer visit — reset the timer to idle to change"
      : "Circuit overseer visit schedule";
    if (state.circuitOverseer && state.circuitOverseerExpiresAt) {
      coHint.textContent = `On — turns off automatically around ${WallClock.formatClock(state.circuitOverseerExpiresAt)}`;
    } else {
      coHint.textContent = "Swaps in the CO visit schedule for 3 hours";
    }

    const schedule = state.schedule || [];
    const index = schedule.findIndex((talk) => talk.id === state.currentTalkId);
    partPosition.textContent = prestart ? (state.prestartLabel || "Meeting starts soon") : index >= 0 ? `Part ${index + 1} of ${schedule.length}` : "Schedule";
    const next = index >= 0 ? schedule[index + 1] : undefined;
    nextPart.textContent = next ? `Next: ${next.title}` : "Last part of the meeting";
    renderPartPicker(schedule, state.currentTalkId);

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
      const state = await WallClock.postJSON(path, body);
      if (state && state.status) {
        render(state);
      }
      return state;
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      console.error(error);
      return null;
    }
  }

  function openAdhocPartPanel() {
    closePartPicker();
    adhocPartTitleInput.value = "";
    adhocPartMinutesInput.value = "5";
    adhocPartPanel.classList.remove("hidden");
    adhocPartBtn.setAttribute("aria-expanded", "true");
    adhocPartTitleInput.focus();
  }

  function closeAdhocPartPanel() {
    adhocPartPanel.classList.add("hidden");
    adhocPartBtn.setAttribute("aria-expanded", "false");
  }

  async function timerCommand(path) {
    try {
      const state = await WallClock.postJSON(path);
      timerCommandPending = false;
      render(state);
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      console.error(error);
      throw error;
    }
  }

  function renderPartPicker(schedule, currentId) {
    const key = JSON.stringify(schedule.map((talk) => [talk.id, talk.title, talk.durationSeconds, talk.temporary ? 1 : 0]));
    if (key !== scheduleKey) {
      scheduleKey = key;
      const armedTalkId = partPickerList.querySelector(".part-picker-option.armed")?.dataset.talkId;
      partPickerList.innerHTML = "";
      schedule.forEach((talk, index) => {
        const row = document.createElement("div");
        row.className = "part-picker-row";
        row.dataset.talkId = String(talk.id);

        const button = document.createElement("button");
        button.className = "part-picker-option";
        button.type = "button";
        button.dataset.talkId = String(talk.id);
        button.innerHTML = `
          <span class="part-picker-label">
            <span>${index + 1}. ${escapeHTML(talk.title)}</span>
            ${talk.temporary ? '<span class="part-badge">Ad hoc</span>' : ""}
          </span>
          <strong>${Math.round(talk.durationSeconds / 60)} min</strong>
        `;
        row.appendChild(button);

        if (talk.temporary) {
          const actions = document.createElement("div");
          actions.className = "part-picker-actions";
          actions.innerHTML = `
            <button class="part-picker-move" type="button" data-move-talk-id="${talk.id}" data-move-delta="-1" ${index === 0 ? "disabled" : ""} aria-label="Move ${escapeAttr(talk.title)} earlier">▲</button>
            <button class="part-picker-move" type="button" data-move-talk-id="${talk.id}" data-move-delta="1" ${index === schedule.length - 1 ? "disabled" : ""} aria-label="Move ${escapeAttr(talk.title)} later">▼</button>
          `;
          row.appendChild(actions);
        }

        partPickerList.appendChild(row);
      });
      if (armedTalkId !== undefined) {
        // Re-arm the pending two-tap confirmation so an SSE schedule update
        // doesn't silently swallow the operator's first tap.
        const rearmed = partPickerList.querySelector(`.part-picker-option[data-talk-id="${armedTalkId}"]`);
        if (rearmed) {
          rearmed.dataset.originalHtml = rearmed.innerHTML;
          rearmed.classList.add("armed");
          rearmed.textContent = "Confirm part";
        }
      }
    }
    const current = schedule.find((talk) => talk.id === currentId);
    currentPartTitle.textContent = current ? current.title : "Select part";
    currentPartDuration.textContent = current ? `${Math.round(current.durationSeconds / 60)} min` : "";
    partPickerList.querySelectorAll(".part-picker-option").forEach((button) => {
      button.classList.toggle("selected", button.dataset.talkId === String(currentId));
    });
  }

  function escapeHTML(value) {
    return String(value).replace(/[&<>"']/g, (char) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#039;",
    })[char]);
  }

  function escapeAttr(value) {
    return escapeHTML(value).replace(/"/g, "&quot;");
  }

  function closePartPicker() {
    partPickerList.classList.add("hidden");
    partPickerBtn.setAttribute("aria-expanded", "false");
  }

  function togglePartPicker() {
    const opening = partPickerList.classList.contains("hidden");
    if (opening) {
      closeAdhocPartPanel();
    }
    partPickerList.classList.toggle("hidden", !opening);
    partPickerBtn.setAttribute("aria-expanded", opening ? "true" : "false");
    if (opening) {
      partPickerList.querySelector(".selected")?.scrollIntoView({ block: "nearest" });
    }
  }


  function disarmReset() {
    clearTimeout(resetArmTimeout);
    resetArmTimeout = null;
    resetBtn.classList.remove("armed");
    resetBtn.textContent = "Reset";
  }

  function disarmPartButtons() {
    clearTimeout(partArmTimeout);
    partArmTimeout = null;
    partPickerList.querySelectorAll(".armed").forEach((button) => {
      button.classList.remove("armed");
      button.innerHTML = button.dataset.originalHtml || button.innerHTML;
    });
  }

  function guardedPartCommand(button, confirmLabel, action) {
    if (latestStatus === "idle") {
      disarmPartButtons();
      action();
      return;
    }
    if (!button.classList.contains("armed")) {
      disarmPartButtons();
      button.classList.add("armed");
      if (!button.dataset.originalHtml) {
        button.dataset.originalHtml = button.innerHTML;
      }
      button.textContent = confirmLabel;
      partArmTimeout = setTimeout(disarmPartButtons, 3000);
      return;
    }
    disarmPartButtons();
    action();
  }

  startBtn.addEventListener("click", async () => {
    if (timerCommandPending) return;
    timerCommandPending = true;
    const status = latestStatus;
    startBtn.disabled = true;
    startBtn.textContent = status === "running" ? "Pausing..." : status === "paused" ? "Resuming..." : "Starting...";
    try {
      await timerCommand(status === "running" ? "/api/control/pause" : "/api/control/start");
    } catch {
      startBtn.disabled = false;
      startBtn.textContent = status === "running" ? "Pause" : status === "paused" ? "Resume" : "Start";
    } finally {
      timerCommandPending = false;
    }
  });
  partPickerBtn.addEventListener("click", togglePartPicker);
  partPickerList.addEventListener("click", (event) => {
    const moveButton = event.target.closest("[data-move-talk-id]");
    if (moveButton) {
      const talkId = Number(moveButton.dataset.moveTalkId);
      const delta = Number(moveButton.dataset.moveDelta);
      command("/api/control/move-part", { talkId, delta });
      return;
    }
    const button = event.target.closest("[data-talk-id]");
    if (!button) return;
    guardedPartCommand(button, "Confirm part", () => {
      closePartPicker();
      command("/api/control/select", { talkId: Number(button.dataset.talkId) });
    });
  });
  adhocPartBtn.addEventListener("click", () => {
    if (adhocPartPanel.classList.contains("hidden")) {
      openAdhocPartPanel();
    } else {
      closeAdhocPartPanel();
    }
  });
  adhocPartPanel.addEventListener("submit", (event) => {
    event.preventDefault();
    const title = adhocPartTitleInput.value.trim() || "Additional Part";
    const minutes = Math.max(1, Math.min(120, Number(adhocPartMinutesInput.value || 5)));
    closeAdhocPartPanel();
    command("/api/control/adhoc-part", { title, seconds: minutes * 60 });
  });
  cancelAdhocPartBtn.addEventListener("click", closeAdhocPartPanel);
  document.addEventListener("click", (event) => {
    if (
      partPickerBtn.contains(event.target) ||
      partPickerList.contains(event.target) ||
      adhocPartBtn.contains(event.target) ||
      adhocPartPanel.contains(event.target)
    ) {
      return;
    }
    closePartPicker();
    closeAdhocPartPanel();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      closePartPicker();
      closeAdhocPartPanel();
    }
  });
  resetBtn.addEventListener("click", () => {
    if (!resetBtn.classList.contains("armed")) {
      resetBtn.classList.add("armed");
      resetBtn.textContent = "Confirm reset";
      resetArmTimeout = setTimeout(disarmReset, 3000);
      return;
    }
    disarmReset();
    command("/api/control/reset");
  });
  document.getElementById("bellBtn").addEventListener("click", () => command("/api/control/bell"));
  coToggle.addEventListener("click", () => {
    const next = !(latestState && latestState.circuitOverseer);
    command("/api/control/circuit-overseer", { on: next });
  });
  document.querySelectorAll("[data-adjust]").forEach((button) => {
    button.addEventListener("click", () => {
      command("/api/control/adjust", { deltaSeconds: Number(button.dataset.adjust) });
    });
  });
  async function init() {
    // Auto-pair from the open pairing endpoint so a printed, tokenless QR
    // (http://hallclock.local/control) works on first scan. The warning only
    // shows if pairing genuinely can't be reached.
    const token = await WallClock.ensureToken();
    if (!token) {
      tokenWarning.classList.remove("hidden");
    }
    WallClock.subscribe(render);
  }
  init();
})();

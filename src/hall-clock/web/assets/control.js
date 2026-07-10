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
  const nextBtn = document.getElementById("nextBtn");
  const resetBtn = document.getElementById("resetBtn");
  const endBtn = document.getElementById("endBtn");
  const partPosition = document.getElementById("partPosition");
  const meetingOvertime = document.getElementById("meetingOvertime");
  const nextPart = document.getElementById("nextPart");
  const statusBadge = document.getElementById("statusBadge");
  const coToggle = document.getElementById("coToggle");
  const coHint = document.getElementById("coHint");
  const languageSelect = document.getElementById("languageSelect");
  const languageStatus = document.getElementById("languageStatus");
  let lastBell = -1;
  let scheduleKey = "";
  let resetArmTimeout = null;
  let nextArmTimeout = null;
  let endArmTimeout = null;
  let partArmTimeout = null;
  let latestStatus = "idle";
  let latestState = null;
  let timerCommandPending = false;
  let languageCommandPending = false;
  let lastAppliedLanguage = "en";

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
    // Restart time and End meeting only act on a live clock, so they are hidden
    // outright while idle rather than shown greyed -- the panel reads as fully
    // available at rest instead of half-disabled.
    const idle = state.status === "idle";
    resetBtn.classList.toggle("hidden", idle);
    if (idle && resetBtn.classList.contains("armed")) {
      disarmReset();
    }
    endBtn.classList.toggle("hidden", idle);
    if (idle && endBtn.classList.contains("armed")) {
      disarmEnd();
    }
    if (state.status === "idle") {
      disarmPartButtons();
      // No live timer left to protect, so the confirmation is moot.
      disarmNext();
    }
    statusBadge.textContent = prestart ? "Countdown" : WallClock.statusLabel(state.status);
    statusBadge.classList.toggle("running", state.status === "running");
    statusBadge.classList.toggle("paused", state.status === "paused");
    statusBadge.classList.toggle("prestart", prestart);
    coToggle.setAttribute("aria-checked", state.circuitOverseer ? "true" : "false");
    const appliedLanguage = state.midweekLanguage || languageFromSchedule(state.schedule) || "en";
    lastAppliedLanguage = appliedLanguage;
    if (languageSelect && !languageCommandPending) {
      languageSelect.value = appliedLanguage;
    }
    if (languageSelect) {
      languageSelect.disabled = languageCommandPending || state.status !== "idle" || state.meetingType === "weekend";
    }
    if (languageStatus && !languageCommandPending && !languageStatus.classList.contains("error")) {
      languageStatus.textContent = state.status === "idle"
        ? ""
        : "Language can be changed while the timer is idle.";
      languageStatus.classList.toggle("hidden", languageStatus.textContent === "");
    }
    // CO mode reshapes the schedule, so it's editable only while idle.
    coToggle.disabled = state.status !== "idle";
    coToggle.title = coToggle.disabled
      ? "Circuit overseer visit — reset the timer to idle to change"
      : "Circuit overseer visit schedule";
    if (state.circuitOverseer && state.circuitOverseerExpiresAt) {
      coHint.textContent = `On — turns off automatically around ${WallClock.formatClock(state.circuitOverseerExpiresAt)}`;
      coHint.classList.remove("hidden");
    } else {
      coHint.textContent = "";
      coHint.classList.add("hidden");
    }

    const schedule = state.schedule || [];
    const index = schedule.findIndex((talk) => talk.id === state.currentTalkId);
    partPosition.textContent = prestart ? (state.prestartLabel || "Meeting starts soon") : index >= 0 ? `Item ${index + 1} of ${schedule.length}` : "Schedule";
    const next = index >= 0 ? schedule[index + 1] : undefined;
    nextPart.textContent = next ? `Next: ${next.title}` : "Last item of the meeting";

    // How far the whole meeting is behind, not just this part. Absent until it
    // exists: a meeting running to time should show nothing at all.
    const behind = state.meetingOvertimeSeconds || 0;
    meetingOvertime.textContent = behind > 0 ? `Meeting ${WallClock.formatTime(behind)} behind` : "";
    meetingOvertime.classList.toggle("hidden", behind <= 0);

    // Nothing follows the last item, so the button retires instead of wrapping.
    // Leave an armed label alone: overwriting it mid-confirmation would drop the
    // operator's first tap on the next state broadcast.
    const atEnd = !next;
    nextBtn.disabled = timerCommandPending || atEnd;
    if (!nextBtn.classList.contains("armed")) {
      nextBtn.textContent = atEnd ? "Meeting complete" : "Next part";
    }
    if (nextBtn.disabled && nextBtn.classList.contains("armed")) {
      disarmNext();
    }

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
      // Only an auth failure means the token is wrong. The server also refuses
      // legitimate requests -- advancing past the last part, changing CO mode
      // mid-meeting -- and telling the operator to re-pair for those sends them
      // chasing a problem that does not exist.
      if (error.status === 401 || error.status === 403) {
        tokenWarning.classList.remove("hidden");
      }
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

  // A person is standing at the front waiting for this, so fail fast rather than
  // hang: the click handler only clears its pending flag once this settles, and
  // render() keeps the button disabled while it is set.
  const TIMER_COMMAND_TIMEOUT_MS = 8000;

  async function timerCommand(path) {
    try {
      const state = await WallClock.postJSON(path, undefined, { timeoutMs: TIMER_COMMAND_TIMEOUT_MS });
      timerCommandPending = false;
      render(state);
    } catch (error) {
      // Only an auth failure means the token is wrong. A timeout or a refusal
      // must not send the operator off to re-pair the device.
      if (error.status === 401 || error.status === 403) {
        tokenWarning.classList.remove("hidden");
      }
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
          rearmed.textContent = "Confirm item";
        }
      }
    }
    const current = schedule.find((talk) => talk.id === currentId);
    currentPartTitle.textContent = current ? current.title : "Select item";
    currentPartDuration.textContent = current ? `${Math.round(current.durationSeconds / 60)} min` : "";
    partPickerList.querySelectorAll(".part-picker-option").forEach((button) => {
      button.classList.toggle("selected", button.dataset.talkId === String(currentId));
    });
  }

  function languageFromSchedule(schedule) {
    const title = (schedule || []).find((talk) => talk.title)?.title || "";
    if (/[áéíóúñü¿¡]/i.test(title)) return "es";
    if (/[ɔɛŋ]/i.test(title)) return "tw";
    return "";
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
    resetBtn.textContent = "Restart time";
  }

  function disarmEnd() {
    clearTimeout(endArmTimeout);
    endArmTimeout = null;
    endBtn.classList.remove("armed");
    endBtn.textContent = "End meeting";
  }

  // The last item has nothing after it, so the button says so rather than
  // looping back to the opening comments.
  function isLastPart(state) {
    const schedule = (state && state.schedule) || [];
    if (schedule.length === 0) return true;
    return schedule[schedule.length - 1].id === state.currentTalkId;
  }

  function disarmNext() {
    clearTimeout(nextArmTimeout);
    nextArmTimeout = null;
    nextBtn.classList.remove("armed");
    nextBtn.textContent = latestState && isLastPart(latestState) ? "Meeting complete" : "Next part";
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
    guardedPartCommand(button, "Confirm item", () => {
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
    const title = adhocPartTitleInput.value.trim() || "Additional item";
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
      resetBtn.textContent = "Confirm restart";
      resetArmTimeout = setTimeout(disarmReset, 3000);
      return;
    }
    disarmReset();
    command("/api/control/reset");
  });
  // Ending a meeting stops the clock, so it always takes two taps -- there is no
  // idle shortcut, since ending while idle is a no-op the button is disabled for.
  endBtn.addEventListener("click", () => {
    if (!endBtn.classList.contains("armed")) {
      endBtn.classList.add("armed");
      endBtn.textContent = "Confirm end";
      endArmTimeout = setTimeout(disarmEnd, 3000);
      return;
    }
    disarmEnd();
    command("/api/control/end");
  });
  // Advancing discards a live timer's elapsed time with no way back, so while a
  // part is running or paused it takes two taps. Idle is the ordinary case
  // (the part just ended) and moves straight on.
  nextBtn.addEventListener("click", () => {
    if (latestStatus === "idle") {
      disarmNext();
      command("/api/control/next");
      return;
    }
    if (!nextBtn.classList.contains("armed")) {
      disarmPartButtons();
      nextBtn.classList.add("armed");
      nextBtn.textContent = "Tap again to end part";
      nextArmTimeout = setTimeout(disarmNext, 3000);
      return;
    }
    disarmNext();
    command("/api/control/next");
  });
  document.getElementById("bellBtn").addEventListener("click", () => command("/api/control/bell"));
  coToggle.addEventListener("click", () => {
    const next = !(latestState && latestState.circuitOverseer);
    command("/api/control/circuit-overseer", { on: next });
  });
  if (languageSelect) {
    languageSelect.addEventListener("change", async () => {
      const language = languageSelect.value;
      languageCommandPending = true;
      languageSelect.disabled = true;
      if (languageStatus) {
        languageStatus.classList.remove("error");
        languageStatus.classList.remove("hidden");
        languageStatus.textContent = `Switching to ${languageName(language)} items...`;
      }
      try {
        const response = await fetch("/api/control/midweek-language", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-Wall-Clock-Token": WallClock.getToken(),
          },
          body: JSON.stringify({ language }),
        });
        if (!response.ok) {
          const message = await response.text();
          throw Object.assign(new Error(message), { status: response.status });
        }
        const state = await response.json();
        render(state);
        if (languageStatus) {
          languageStatus.classList.remove("error");
          languageStatus.classList.remove("hidden");
          languageStatus.textContent = `${languageName(language)} items applied.`;
        }
      } catch (error) {
        console.error(error);
        if (languageStatus) {
          if (error.status === 401 || error.status === 403) {
            tokenWarning.classList.remove("hidden");
            languageStatus.classList.add("error");
            languageStatus.classList.remove("hidden");
            languageStatus.textContent = `Pair this phone before changing languages.`;
          } else {
            languageStatus.classList.add("error");
            languageStatus.classList.remove("hidden");
            languageStatus.textContent = `Could not switch to ${languageName(language)}. ${cleanError(error.message)}`;
          }
        }
      } finally {
        languageCommandPending = false;
        if (latestState) {
          render(latestState);
        }
      }
    });
  }

  function languageName(language) {
    if (language === "es") return "Spanish";
    if (language === "tw") return "Twi";
    return "English";
  }

  function cleanError(message) {
    return String(message || "").trim().replace(/\s+/g, " ");
  }
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

(function () {
  const tokenWarning = document.getElementById("tokenWarning");
  const form = document.getElementById("setupForm");
  const deviceNameInput = document.getElementById("deviceNameInput");
  const advertisedBaseUrlInput = document.getElementById("advertisedBaseUrlInput");
  const meetingTypeInput = document.getElementById("meetingTypeInput");
  const prestartMinutesInput = document.getElementById("prestartMinutesInput");
  const midweekUrlInput = document.getElementById("midweekUrlInput");
  const autoImportInput = document.getElementById("autoImportInput");
  const autoImportStatus = document.getElementById("autoImportStatus");
  const scheduleModeText = document.getElementById("scheduleModeText");
  const startsList = document.getElementById("startsList");
  const partsList = document.getElementById("partsList");
  const saveStatus = document.getElementById("saveStatus");
  const tabButtons = Array.from(document.querySelectorAll("[data-settings-tab]"));
  const tabPanels = Array.from(document.querySelectorAll("[data-settings-panel]"));
  let parts = [];
  let meetingStarts = [];
  let defaultScheduleLanguage = "en";
  const dayLabels = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

  async function load() {
    const config = await fetchConfig();
    deviceNameInput.value = config.deviceName || "Hall Clock";
    advertisedBaseUrlInput.value = config.advertisedBaseUrl || "";
    meetingTypeInput.value = config.meetingType || "midweek";
    renderMeetingType(config.meetingType || "midweek");
    prestartMinutesInput.value = Math.round((config.prestartSeconds || 300) / 60);
    midweekUrlInput.value = config.midweekUrl || "";
    defaultScheduleLanguage = normalizeLanguage(config.midweekLanguage) || languageFromURL(config.midweekUrl) || "en";
    autoImportInput.checked = Boolean(config.autoImportMidweek);
    renderAutoStatus(config);
    meetingStarts = config.meetingStarts || defaultMeetingStarts(config.meetingStartTime || "19:30");
    parts = config.schedule || [];
    renderStarts();
    renderParts();
  }

  async function fetchConfig() {
    const response = await fetch("/api/config");
    return response.json();
  }

  async function refreshMeetingType() {
    const config = await fetchConfig();
    meetingTypeInput.value = config.meetingType || "midweek";
    renderMeetingType(meetingTypeInput.value);
  }

  function renderAutoStatus(config) {
    if (config.midweekImportedWeek) {
      const match = /^(\d{4})-W(\d{2})$/.exec(config.midweekImportedWeek);
      const week = match ? `week ${Number(match[2])} of ${match[1]}` : config.midweekImportedWeek;
      autoImportStatus.textContent = `Last imported ${week}${config.midweekUrl ? ` from ${config.midweekUrl}` : ""}.`;
    } else {
      autoImportStatus.textContent = "Nothing imported yet.";
    }
  }

  function renderMeetingType(meetingType) {
    const title = meetingType === "weekend" ? "Weekend meeting is active today" : "Midweek meeting is active today";
    if (scheduleModeText) scheduleModeText.textContent = title;
  }

  function activateTab(name, focus) {
    tabButtons.forEach((button) => {
      const active = button.dataset.settingsTab === name;
      button.classList.toggle("active", active);
      button.setAttribute("aria-selected", String(active));
      button.tabIndex = active ? 0 : -1;
      if (active && focus) button.focus();
    });
    tabPanels.forEach((panel) => {
      const active = panel.dataset.settingsPanel === name;
      panel.classList.toggle("active", active);
      panel.hidden = !active;
    });
  }

  function watchAutoImport(attempts) {
    setTimeout(async () => {
      try {
        const config = await fetchConfig();
        renderAutoStatus(config);
        renderMeetingType(config.meetingType || "midweek");
        if (config.midweekUrl) midweekUrlInput.value = config.midweekUrl;
        if (config.midweekImportedWeek) {
          parts = config.schedule || parts;
          renderParts();
        } else if (attempts > 1) {
          watchAutoImport(attempts - 1);
        }
      } catch (error) {
        console.error(error);
      }
    }, 3000);
  }

  function defaultMeetingStarts(time) {
    const starts = [1, 2, 3, 4, 5].map((day, index) => ({
      id: index + 1,
      day,
      time,
      congregation: "",
      language: "en",
      midweekUrl: "",
    }));
    starts.push({ id: starts.length + 1, day: 0, time: "10:00", congregation: "", language: "en", midweekUrl: "" });
    return starts;
  }

  function renderStarts() {
    startsList.innerHTML = "";
    meetingStarts.forEach((start, index) => {
      const row = document.createElement("div");
      row.className = "start-row";
      row.innerHTML = `
        <label class="field">
          <span>Day</span>
          <select data-start-field="day" data-index="${index}">
            ${dayLabels.map((label, day) => `<option value="${day}" ${Number(start.day) === day ? "selected" : ""}>${label}</option>`).join("")}
          </select>
        </label>
        <label class="field">
          <span>Time</span>
          <input data-start-field="time" data-index="${index}" type="time" value="${escapeAttr(start.time || "19:30")}">
        </label>
        <label class="field">
          <span>Schedule language</span>
          <select data-start-field="language" data-index="${index}">
            ${languageOptions(start.language || languageFromURL(start.midweekUrl) || defaultScheduleLanguage)}
          </select>
        </label>
        <button data-remove-start="${index}" class="row-remove" type="button" aria-label="Remove this start time">Remove</button>
      `;
      startsList.appendChild(row);
    });
  }

  function readStartsFromForm() {
    startsList.querySelectorAll("[data-start-field]").forEach((input) => {
      const index = Number(input.dataset.index);
      const field = input.dataset.startField;
      if (field === "day") meetingStarts[index].day = Number(input.value);
      if (field === "time") meetingStarts[index].time = input.value;
      if (field === "language") {
        meetingStarts[index].language = input.value;
        meetingStarts[index].congregation = "";
        meetingStarts[index].midweekUrl = "";
        meetingStarts[index].midweekImportedWeek = "";
      }
    });
  }

  function languageOptions(selected) {
    return [
      ["en", "English"],
      ["es", "Spanish"],
      ["tw", "Twi"],
    ].map(([value, label]) => `<option value="${value}" ${selected === value ? "selected" : ""}>${label}</option>`).join("");
  }

  function languageFromURL(value) {
    const match = String(value || "").match(/^https?:\/\/wol\.jw\.org\/([^/]+)\//);
    const language = match ? match[1] : "";
    if (language === "es") return "es";
    if (language === "tw") return "tw";
    return language === "en" ? "en" : "";
  }

  function normalizeLanguage(value) {
    const language = String(value || "").trim().toLowerCase();
    if (language === "en" || language === "english") return "en";
    if (language === "es" || language === "spanish") return "es";
    if (language === "tw" || language === "twi") return "tw";
    return "";
  }

  function renderParts() {
    partsList.innerHTML = "";
    parts.forEach((part, index) => {
      const row = document.createElement("div");
      row.className = "part-row";
      row.innerHTML = `
        <input
          class="part-input part-title"
          data-field="title"
          data-index="${index}"
          type="text"
          value="${escapeAttr(part.title)}"
          aria-label="Item title"
          placeholder="Item title"
        >
        <span class="part-caption" aria-hidden="true">Minutes</span>
        <input
          class="part-input part-minutes"
          data-field="minutes"
          data-index="${index}"
          type="number"
          min="1"
          max="120"
          inputmode="numeric"
          value="${Math.round(part.durationSeconds / 60)}"
          aria-label="Minutes"
        >
        <span class="part-caption" aria-hidden="true">Closing bell</span>
        <span class="part-readonly" title="Set by the WOL import: seconds of amber warning before time is up">
          ${Number(part.closingSeconds) || 0}s
        </span>
        <button data-remove="${index}" class="row-remove" type="button" aria-label="Remove ${escapeAttr(part.title)}">Remove</button>
      `;
      partsList.appendChild(row);
    });
  }

  // The closing bell is displayed but not editable: the WOL import defines it,
  // and the server restores it on every save (applyImportedClosingSeconds), so
  // there is nothing to read back out of the form for it.
  function readPartsFromForm() {
    partsList.querySelectorAll("input").forEach((input) => {
      const index = Number(input.dataset.index);
      const field = input.dataset.field;
      if (field === "title") parts[index].title = input.value;
      if (field === "minutes") parts[index].durationSeconds = Number(input.value) * 60;
    });
  }

  function escapeAttr(value) {
    return String(value || "").replace(/[&<>"']/g, (char) => ({
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#039;",
    }[char]));
  }

  document.getElementById("addPartBtn").addEventListener("click", () => {
    readPartsFromForm();
    parts.push({ title: `Item ${parts.length + 1}`, durationSeconds: 300, closingSeconds: 120 });
    renderParts();
  });

  tabButtons.forEach((button, index) => {
    button.addEventListener("click", () => activateTab(button.dataset.settingsTab, false));
    button.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      let nextIndex = index;
      if (event.key === "ArrowLeft") nextIndex = index === 0 ? tabButtons.length - 1 : index - 1;
      if (event.key === "ArrowRight") nextIndex = index === tabButtons.length - 1 ? 0 : index + 1;
      if (event.key === "Home") nextIndex = 0;
      if (event.key === "End") nextIndex = tabButtons.length - 1;
      activateTab(tabButtons[nextIndex].dataset.settingsTab, true);
    });
  });

  document.getElementById("addStartBtn").addEventListener("click", () => {
    readStartsFromForm();
    const last = meetingStarts[meetingStarts.length - 1];
    meetingStarts.push({
      id: meetingStarts.length + 1,
      day: last ? Number(last.day) : 1,
      time: last ? last.time : "19:30",
      congregation: "",
      language: "en",
      midweekUrl: "",
    });
    renderStarts();
  });

  startsList.addEventListener("click", (event) => {
    const index = event.target.dataset.removeStart;
    if (index === undefined) return;
    readStartsFromForm();
    meetingStarts.splice(Number(index), 1);
    if (meetingStarts.length === 0) {
      meetingStarts = defaultMeetingStarts("19:30");
    }
    renderStarts();
  });

  document.getElementById("parseMidweekBtn").addEventListener("click", () => {
    importMidweekText(false);
  });

  document.getElementById("previewMidweekUrlBtn").addEventListener("click", async () => {
    await importMidweekUrl(false);
  });

  document.getElementById("applyMidweekUrlBtn").addEventListener("click", async () => {
    await importMidweekUrl(true);
  });

  async function importMidweekUrl(apply) {
    saveStatus.textContent = apply ? "Importing..." : "Fetching preview...";
    try {
      const result = await WallClock.postJSON("/api/import/midweek", {
        url: midweekUrlInput.value,
        apply,
      });
      parts = result.schedule || [];
      renderParts();
      if (apply) {
        await refreshMeetingType();
      }
      tokenWarning.classList.add("hidden");
      saveStatus.textContent = apply ? "Imported and saved" : `Previewed ${parts.length} items`;
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      saveStatus.textContent = "Could not import URL";
      console.error(error);
    }
  }

  async function importMidweekText(apply) {
    saveStatus.textContent = apply ? "Importing pasted timings..." : "Parsing pasted timings...";
    try {
      const result = await WallClock.postJSON("/api/import/midweek-text", {
        text: document.getElementById("midweekTextInput").value,
        apply,
      });
      parts = result.schedule || [];
      renderParts();
      if (apply) {
        await refreshMeetingType();
      }
      tokenWarning.classList.add("hidden");
      saveStatus.textContent = apply ? "Imported and saved" : `Parsed ${parts.length} items`;
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      saveStatus.textContent = "Could not parse pasted timings";
      console.error(error);
    }
  }

  partsList.addEventListener("click", (event) => {
    const index = event.target.dataset.remove;
    if (index === undefined) return;
    readPartsFromForm();
    parts.splice(Number(index), 1);
    if (parts.length === 0) {
      parts.push({ title: "Item 1", durationSeconds: 300, closingSeconds: 120 });
    }
    renderParts();
  });

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    readPartsFromForm();
    readStartsFromForm();
    saveStatus.textContent = "Saving...";
    try {
      await WallClock.postJSON("/api/config", {
        deviceName: deviceNameInput.value,
        advertisedBaseUrl: advertisedBaseUrlInput.value,
        meetingType: meetingTypeInput.value,
        meetingStartTime: meetingStarts[0]?.time || "19:30",
        meetingStarts,
        prestartSeconds: Number(prestartMinutesInput.value || 5) * 60,
        midweekUrl: midweekUrlInput.value,
        autoImportMidweek: autoImportInput.checked,
        schedule: parts,
      });
      // The server owns the closing bell and may re-derive it, so redraw from
      // the saved midweek program rather than leave a stale number on screen.
      // The POST response carries the runtime state (on a weekend, the weekend
      // template), which must never be loaded into this editor.
      const savedConfig = await fetchConfig();
      parts = savedConfig.schedule || parts;
      renderParts();
      saveStatus.textContent = "Saved";
      tokenWarning.classList.add("hidden");
      if (autoImportInput.checked) {
        watchAutoImport(15);
      }
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      saveStatus.textContent = "Could not save";
      console.error(error);
    }
  });

  if (!WallClock.getToken()) {
    tokenWarning.classList.remove("hidden");
  }

  load();
})();

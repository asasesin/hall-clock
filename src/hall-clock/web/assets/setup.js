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
  const startsList = document.getElementById("startsList");
  const partsList = document.getElementById("partsList");
  const saveStatus = document.getElementById("saveStatus");
  let parts = [];
  let meetingStarts = [];
  const dayLabels = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

  async function load() {
    const response = await fetch("/api/config");
    const config = await response.json();
    deviceNameInput.value = config.deviceName || "Hall Clock";
    advertisedBaseUrlInput.value = config.advertisedBaseUrl || "";
    meetingTypeInput.value = config.meetingType || "midweek";
    prestartMinutesInput.value = Math.round((config.prestartSeconds || 300) / 60);
    midweekUrlInput.value = config.midweekUrl || "";
    autoImportInput.checked = Boolean(config.autoImportMidweek);
    renderAutoStatus(config);
    meetingStarts = config.meetingStarts || defaultMeetingStarts(config.meetingStartTime || "19:30");
    parts = config.schedule || [];
    renderStarts();
    renderParts();
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

  function watchAutoImport(attempts) {
    setTimeout(async () => {
      try {
        const config = await (await fetch("/api/config")).json();
        renderAutoStatus(config);
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
    }));
    starts.push({ id: starts.length + 1, day: 0, time: "10:00", congregation: "" });
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
          <span>Congregation</span>
          <input data-start-field="congregation" data-index="${index}" type="text" value="${escapeAttr(start.congregation || "")}" placeholder="Optional">
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
      if (field === "congregation") meetingStarts[index].congregation = input.value;
    });
  }

  function renderParts() {
    partsList.innerHTML = "";
    parts.forEach((part, index) => {
      const row = document.createElement("div");
      row.className = "part-row";
      row.innerHTML = `
        <label class="field">
          <span>Title</span>
          <input data-field="title" data-index="${index}" type="text" value="${escapeAttr(part.title)}">
        </label>
        <label class="field">
          <span>Minutes</span>
          <input data-field="minutes" data-index="${index}" type="number" min="1" max="120" value="${Math.round(part.durationSeconds / 60)}">
        </label>
        <label class="field">
          <span>Closing bell (sec)</span>
          <input data-field="closingSeconds" data-index="${index}" type="number" min="0" max="600" value="${part.closingSeconds}">
        </label>
        <button data-remove="${index}" class="row-remove" type="button" aria-label="Remove ${escapeAttr(part.title)}">Remove</button>
      `;
      partsList.appendChild(row);
    });
  }

  function readPartsFromForm() {
    partsList.querySelectorAll("input").forEach((input) => {
      const index = Number(input.dataset.index);
      const field = input.dataset.field;
      if (field === "title") parts[index].title = input.value;
      if (field === "minutes") parts[index].durationSeconds = Number(input.value) * 60;
      if (field === "closingSeconds") parts[index].closingSeconds = Number(input.value);
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
    parts.push({ title: `Part ${parts.length + 1}`, durationSeconds: 300, closingSeconds: 60 });
    renderParts();
  });

  document.getElementById("addStartBtn").addEventListener("click", () => {
    readStartsFromForm();
    const last = meetingStarts[meetingStarts.length - 1];
    meetingStarts.push({
      id: meetingStarts.length + 1,
      day: last ? Number(last.day) : 1,
      time: last ? last.time : "19:30",
      congregation: "",
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

  document.getElementById("midweekTemplateBtn").addEventListener("click", async () => {
    await applyTemplate("/api/template/midweek");
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
      meetingTypeInput.value = result.meetingType || "midweek";
      parts = result.schedule || [];
      renderParts();
      tokenWarning.classList.add("hidden");
      saveStatus.textContent = apply ? "Imported and saved" : `Previewed ${parts.length} parts`;
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
      meetingTypeInput.value = result.meetingType || "midweek";
      parts = result.schedule || [];
      renderParts();
      tokenWarning.classList.add("hidden");
      saveStatus.textContent = apply ? "Imported and saved" : `Parsed ${parts.length} parts`;
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      saveStatus.textContent = "Could not parse pasted timings";
      console.error(error);
    }
  }

  async function applyTemplate(path) {
    saveStatus.textContent = "Applying template...";
    try {
      const state = await WallClock.postJSON(path);
      deviceNameInput.value = state.deviceName || deviceNameInput.value;
      meetingTypeInput.value = state.meetingType || "midweek";
      meetingStarts = state.meetingStarts || meetingStarts;
      renderStarts();
      parts = state.schedule || [];
      renderParts();
      tokenWarning.classList.add("hidden");
      saveStatus.textContent = "Template applied";
    } catch (error) {
      tokenWarning.classList.remove("hidden");
      saveStatus.textContent = "Could not apply template";
      console.error(error);
    }
  }

  partsList.addEventListener("click", (event) => {
    const index = event.target.dataset.remove;
    if (index === undefined) return;
    readPartsFromForm();
    parts.splice(Number(index), 1);
    if (parts.length === 0) {
      parts.push({ title: "Part 1", durationSeconds: 300, closingSeconds: 60 });
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

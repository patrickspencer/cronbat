const params = new URLSearchParams(window.location.search);
const jobName = params.get("name");

const titleEl = document.getElementById("job-title");
const settingsLinkEl = document.getElementById("job-settings-link");
const statusEl = document.getElementById("status");
const limitEl = document.getElementById("limit");
const refreshBtn = document.getElementById("refresh-btn");
const runsBodyEl = document.getElementById("runs-body");
const runMetaEl = document.getElementById("run-meta");
const stdoutEl = document.getElementById("stdout");
const stderrEl = document.getElementById("stderr");

const FALLBACK_POLL_INTERVAL_MS = 15000;

let currentRuns = [];
let selectedRunID = "";
let runsLoadInFlight = false;
let detailLoadInFlight = false;
let pollHandle = null;
let eventStream = null;

function setStatus(message, isError = false) {
  statusEl.textContent = message;
  statusEl.classList.toggle("error", isError);
}

function formatDate(value) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function formatDuration(ms) {
  if (typeof ms !== "number" || ms < 0) {
    return "-";
  }
  return `${ms} ms`;
}

async function api(path) {
  const response = await fetch(path);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `request failed (${response.status})`);
  }
  return payload;
}

async function loadRunDetail(runID, options = {}) {
  if (!runID || detailLoadInFlight) {
    return;
  }

  const silent = Boolean(options.silent);
  detailLoadInFlight = true;
  if (!silent) {
    setStatus(`Loading run ${runID}...`);
  }

  try {
    const payload = await api(`/api/v1/runs/${encodeURIComponent(runID)}/logs`);
    const sourceLabel = payload.source === "file" ? "persisted file" : "tail from database";
    const storageWarning = payload.storage_error ? ` | warning: ${payload.storage_error}` : "";

    runMetaEl.textContent =
      `Run ${payload.run_id} | source: ${sourceLabel}${storageWarning}`;
    stdoutEl.textContent = payload.stdout || "";
    stderrEl.textContent = payload.stderr || "";
    if (!silent) {
      setStatus(`Loaded run ${runID}`);
    }
  } catch (err) {
    runMetaEl.textContent = `Failed to load run ${runID}`;
    stdoutEl.textContent = "";
    stderrEl.textContent = "";
    setStatus(err.message, true);
  } finally {
    detailLoadInFlight = false;
  }
}

function renderRuns() {
  runsBodyEl.innerHTML = "";
  if (currentRuns.length === 0) {
    runsBodyEl.innerHTML = `<tr><td colspan="6">No runs found for ${jobName}</td></tr>`;
    runMetaEl.textContent = "No runs available.";
    stdoutEl.textContent = "";
    stderrEl.textContent = "";
    return;
  }

  for (const run of currentRuns) {
    const tr = document.createElement("tr");
    if (run.id === selectedRunID) {
      tr.classList.add("active");
    }

    tr.innerHTML = `
      <td>${formatDate(run.started_at)}</td>
      <td>${run.status || "-"}</td>
      <td>${run.trigger || "-"}</td>
      <td>${formatDuration(run.duration_ms)}</td>
      <td>${run.exit_code}</td>
      <td><a class="button-link" href="/ui/run.html?id=${encodeURIComponent(run.id)}">Open</a></td>
    `;

    tr.addEventListener("click", (event) => {
      const target = event.target;
      if (target && target.closest("a")) {
        return;
      }
      selectedRunID = run.id;
      renderRuns();
      loadRunDetail(selectedRunID, { silent: true });
    });

    runsBodyEl.appendChild(tr);
  }
}

async function loadRuns(options = {}) {
  if (runsLoadInFlight) {
    return;
  }
  runsLoadInFlight = true;
  const silent = Boolean(options.silent);
  if (!silent) {
    setStatus("Loading runs...");
  }

  try {
    const limit = Number(limitEl.value || "50");
    const runs = await api(`/api/v1/runs?job=${encodeURIComponent(jobName)}&limit=${limit}`);
    currentRuns = Array.isArray(runs) ? runs : [];

    if (selectedRunID && !currentRuns.some((r) => r.id === selectedRunID)) {
      selectedRunID = "";
    }
    if (!selectedRunID && currentRuns.length > 0) {
      selectedRunID = currentRuns[0].id;
    }

    renderRuns();
    if (selectedRunID) {
      await loadRunDetail(selectedRunID, { silent: true });
    }
    if (!silent) {
      setStatus(`Loaded ${currentRuns.length} run(s).`);
    }
  } catch (err) {
    setStatus(err.message, true);
  } finally {
    runsLoadInFlight = false;
  }
}

function startFallbackPolling() {
  if (pollHandle) {
    clearInterval(pollHandle);
  }
  pollHandle = setInterval(() => {
    if (document.hidden) {
      return;
    }
    loadRuns({ silent: true });
  }, FALLBACK_POLL_INTERVAL_MS);
}

function onRealtimeEvent(event) {
  let payload = null;
  try {
    payload = JSON.parse(event.data);
  } catch (_) {
    payload = null;
  }

  if (payload && payload.job_name && payload.job_name !== jobName) {
    return;
  }
  loadRuns({ silent: true });
}

function startEventStream() {
  if (!("EventSource" in window)) {
    return;
  }
  if (eventStream) {
    eventStream.close();
  }

  eventStream = new EventSource("/api/v1/events");
  eventStream.addEventListener("run.started", onRealtimeEvent);
  eventStream.addEventListener("run.completed", onRealtimeEvent);
  eventStream.addEventListener("job.changed", onRealtimeEvent);
}

function disablePage(msg) {
  setStatus(msg, true);
  refreshBtn.disabled = true;
  limitEl.disabled = true;
}

refreshBtn.addEventListener("click", () => loadRuns());
limitEl.addEventListener("change", () => loadRuns());

document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    loadRuns({ silent: true });
  }
});

async function init() {
  if (!jobName) {
    disablePage("Missing job name in URL query (?name=...)");
    return;
  }

  titleEl.textContent = `Logs: ${jobName}`;
  settingsLinkEl.href = `/ui/job.html?name=${encodeURIComponent(jobName)}`;

  await loadRuns();
  startEventStream();
  startFallbackPolling();
}

init();

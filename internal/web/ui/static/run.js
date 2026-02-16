const params = new URLSearchParams(window.location.search);
const runID = params.get("id");

const runTitleEl = document.getElementById("run-title");
const runSubtitleEl = document.getElementById("run-subtitle");
const runSettingsLinkEl = document.getElementById("run-settings-link");
const runLogsLinkEl = document.getElementById("run-logs-link");
const statusEl = document.getElementById("status");
const metaEl = document.getElementById("meta");
const stdoutEl = document.getElementById("stdout");
const stderrEl = document.getElementById("stderr");

let refreshHandle = null;
let lastRun = null;

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

async function api(path) {
  const response = await fetch(path);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `request failed (${response.status})`);
  }
  return payload;
}

function renderMeta(run, logs) {
  const source = logs.source === "file" ? "persisted file" : "database tail";
  metaEl.textContent = [
    `Run ID: ${run.id}`,
    `Job: ${run.job_name}`,
    `Status: ${run.status}`,
    `Trigger: ${run.trigger}`,
    `Started: ${formatDate(run.started_at)}`,
    `Finished: ${formatDate(run.finished_at)}`,
    `Duration: ${run.duration_ms} ms`,
    `Exit Code: ${run.exit_code}`,
    `Log Source: ${source}`
  ].join("\n");
}

async function loadRun() {
  if (!runID) {
    setStatus("Missing run id in URL query (?id=...)", true);
    return;
  }

  try {
    const run = await api(`/api/v1/runs/${encodeURIComponent(runID)}`);
    const logs = await api(`/api/v1/runs/${encodeURIComponent(runID)}/logs`);
    lastRun = run;

    runTitleEl.textContent = `Run: ${run.id}`;
    runSubtitleEl.textContent = `${run.job_name} | ${run.status} | ${formatDate(run.started_at)}`;
    runSettingsLinkEl.href = `/ui/job.html?name=${encodeURIComponent(run.job_name)}`;
    runLogsLinkEl.href = `/ui/logs.html?name=${encodeURIComponent(run.job_name)}`;

    renderMeta(run, logs);
    stdoutEl.textContent = logs.stdout || "";
    stderrEl.textContent = logs.stderr || "";
    setStatus("Run loaded");
  } catch (err) {
    setStatus(err.message, true);
  }
}

function startAutoRefresh() {
  if (refreshHandle) {
    clearInterval(refreshHandle);
  }

  refreshHandle = setInterval(() => {
    if (document.hidden) {
      return;
    }
    // Keep refreshing only while run is active.
    if (lastRun && lastRun.status !== "running") {
      clearInterval(refreshHandle);
      refreshHandle = null;
      return;
    }
    loadRun();
  }, 3000);
}

loadRun().then(startAutoRefresh);

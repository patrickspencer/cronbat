const statusEl = document.getElementById("status");
const jobsBodyEl = document.getElementById("jobs-body");
const refreshBtn = document.getElementById("refresh-btn");
const deleteModalEl = document.getElementById("delete-modal");
const deleteJobNameEl = document.getElementById("delete-job-name");
const deleteInputEl = document.getElementById("delete-confirm-input");
const deleteConfirmBtn = document.getElementById("delete-confirm-btn");
const deleteCancelBtn = document.getElementById("delete-cancel-btn");
const FALLBACK_POLL_INTERVAL_MS = 15000;

let loadInFlight = false;
let hasLoadedOnce = false;
let pollHandle = null;
let eventStream = null;
let streamConnected = false;
let pendingDeleteJobName = "";

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

function escapeHTML(input) {
  return String(input ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function pluralize(value, unit) {
  if (value === 1) {
    return `${value} ${unit}`;
  }
  return `${value} ${unit}s`;
}

function pad2(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) {
    return value;
  }
  return String(n).padStart(2, "0");
}

function describeDayOfWeek(value) {
  const labels = {
    "0": "Sunday",
    "1": "Monday",
    "2": "Tuesday",
    "3": "Wednesday",
    "4": "Thursday",
    "5": "Friday",
    "6": "Saturday",
    "7": "Sunday"
  };
  if (labels[value]) {
    return labels[value];
  }
  return value;
}

function describeSchedule(expr) {
  if (!expr) {
    return "No schedule";
  }

  const value = expr.trim();
  const shortcuts = {
    "@yearly": "every year",
    "@annually": "every year",
    "@monthly": "every month",
    "@weekly": "every week",
    "@daily": "every day",
    "@midnight": "every day at 00:00",
    "@hourly": "every hour"
  };
  if (shortcuts[value]) {
    return shortcuts[value];
  }

  const parts = value.split(/\s+/);
  if (parts.length !== 5) {
    return "Custom schedule";
  }

  const [min, hour, dayOfMonth, month, dayOfWeek] = parts;
  const allWild = min === "*" && hour === "*" && dayOfMonth === "*" && month === "*" && dayOfWeek === "*";
  if (allWild) {
    return "Every minute";
  }

  if (min.startsWith("*/") && hour === "*" && dayOfMonth === "*" && month === "*" && dayOfWeek === "*") {
    const step = Number(min.slice(2));
    if (Number.isFinite(step) && step > 0) {
      return `Every ${pluralize(step, "minute")}`;
    }
  }

  if (/^\d+$/.test(min) && hour === "*" && dayOfMonth === "*" && month === "*" && dayOfWeek === "*") {
    return `Every hour at minute ${pad2(min)}`;
  }

  if (/^\d+$/.test(min) && /^\d+$/.test(hour) && dayOfMonth === "*" && month === "*" && dayOfWeek === "*") {
    return `Every day at ${pad2(hour)}:${pad2(min)}`;
  }

  if (/^\d+$/.test(min) && /^\d+$/.test(hour) && dayOfMonth.startsWith("*/") && month === "*" && dayOfWeek === "*") {
    const step = Number(dayOfMonth.slice(2));
    if (Number.isFinite(step) && step > 0) {
      return `Every ${pluralize(step, "day")} at ${pad2(hour)}:${pad2(min)}`;
    }
  }

  if (/^\d+$/.test(min) && /^\d+$/.test(hour) && dayOfMonth === "*" && month === "*" && dayOfWeek !== "*") {
    return `Every ${describeDayOfWeek(dayOfWeek)} at ${pad2(hour)}:${pad2(min)}`;
  }

  return "Custom schedule";
}

function resolveState(job) {
  const raw = String(job.state || "").toLowerCase();
  if (raw === "started" || raw === "paused" || raw === "stopped") {
    return raw;
  }
  return job.enabled ? "started" : "stopped";
}

function resolveLastRunStatus(status) {
  const raw = String(status || "").toLowerCase();
  if (raw === "success" || raw === "failure" || raw === "running") {
    return raw;
  }
  return "none";
}

function labelForState(state) {
  if (state === "started") {
    return "active";
  }
  if (state === "stopped") {
    return "inactive";
  }
  return state;
}

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `request failed (${response.status})`);
  }
  return payload;
}

async function callAction(jobName, action, confirmMessage) {
  if (confirmMessage && !window.confirm(confirmMessage)) {
    return;
  }

  const path = `/api/v1/jobs/${encodeURIComponent(jobName)}${action ? `/${action}` : ""}`;
  const method = action === "" ? "DELETE" : "PUT";

  setStatus(`Running ${action || "delete"} on ${jobName}...`);
  try {
    await api(path, { method });
    setStatus(`Updated ${jobName}`);
    await loadJobs();
  } catch (err) {
    setStatus(err.message, true);
  }
}

async function copyJob(jobName) {
  const defaultName = `${jobName}_copy`;
  const userInput = window.prompt("Name for copied job:", defaultName);
  if (userInput === null) {
    return;
  }

  const newName = userInput.trim();
  if (!newName) {
    setStatus("Copy name cannot be empty", true);
    return;
  }

  setStatus(`Copying ${jobName} to ${newName}...`);
  try {
    const src = await api(`/api/v1/jobs/${encodeURIComponent(jobName)}`);
    const payload = {
      name: newName,
      schedule: src.schedule || "",
      command: src.command || "",
      working_dir: src.working_dir || "",
      executor: src.executor || "shell",
      timeout: src.timeout || "",
      enabled: Boolean(src.enabled),
      env: src.env || {},
      on_success: Array.isArray(src.on_success) ? src.on_success : [],
      on_failure: Array.isArray(src.on_failure) ? src.on_failure : [],
      metadata: src.metadata && typeof src.metadata === "object" ? src.metadata : {}
    };
    await api("/api/v1/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
    setStatus(`Copied ${jobName} to ${newName}`);
    await loadJobs();
  } catch (err) {
    setStatus(err.message, true);
  }
}

function setDeleteConfirmEnabled() {
  if (!deleteConfirmBtn || !deleteInputEl) {
    return;
  }
  deleteConfirmBtn.disabled = deleteInputEl.value.trim().toLowerCase() !== "delete";
}

function closeDeleteModal() {
  if (!deleteModalEl) {
    return;
  }
  deleteModalEl.classList.remove("open");
  deleteModalEl.setAttribute("aria-hidden", "true");
  pendingDeleteJobName = "";
  if (deleteInputEl) {
    deleteInputEl.value = "";
  }
  setDeleteConfirmEnabled();
}

function openDeleteModal(jobName) {
  if (!deleteModalEl || !deleteJobNameEl || !deleteInputEl) {
    return;
  }
  pendingDeleteJobName = jobName;
  deleteJobNameEl.textContent = jobName;
  deleteInputEl.value = "";
  setDeleteConfirmEnabled();
  deleteModalEl.classList.add("open");
  deleteModalEl.setAttribute("aria-hidden", "false");
  deleteInputEl.focus();
}

function renderJob(job) {
  const tr = document.createElement("tr");
  const state = resolveState(job);
  const humanSchedule = describeSchedule(job.schedule);
  const nextRun = formatDate(job.next_run);
  const lastRun = formatDate(job.last_run);
  const lastRunStatus = resolveLastRunStatus(job.last_run_status);
  const lastRunStatusLabel = lastRunStatus === "none" ? "no runs" : lastRunStatus;
  const stateLabel = labelForState(state);
  const jobDetailURL = `/ui/job.html?name=${encodeURIComponent(job.name)}`;
  const safeName = escapeHTML(job.name || "-");
  const safeSchedule = escapeHTML(job.schedule || "-");
  const safeHumanSchedule = escapeHTML(humanSchedule);
  const safeCommand = escapeHTML(job.command || "-");
  const safeNextRun = escapeHTML(nextRun);
  const safeLastRun = escapeHTML(lastRun);
  const safeLastRunStatus = escapeHTML(lastRunStatusLabel);
  const safeStateLabel = escapeHTML(stateLabel);

  tr.innerHTML = `
    <td><strong><a class="job-title-link" href="${jobDetailURL}">${safeName}</a></strong></td>
    <td>
      <div class="status-cell-simple">
        <span class="status-pill ${state}">${safeStateLabel}</span>
      </div>
    </td>
    <td>
      <div class="run-status-cell">
        <div class="status-head">
          <span class="run-pill ${lastRunStatus}">${safeLastRunStatus}</span>
        </div>
        <div class="status-meta"><strong>Next:</strong> ${safeNextRun}</div>
        <div class="status-meta"><strong>Last:</strong> ${safeLastRun}</div>
      </div>
    </td>
    <td>
      <div class="job-actions">
        <button class="mini-btn" data-action="start">Start</button>
        <button class="mini-btn" data-action="stop">Stop</button>
        <button class="mini-btn" data-action="run">Run</button>
        <button class="mini-btn" data-action="logs">Logs</button>
        <button class="mini-btn" data-action="edit">Edit</button>
        <button class="mini-btn" data-action="copy">Copy</button>
        <button class="mini-btn" data-action="archive">Archive</button>
        <button class="mini-btn danger delete-btn" data-action="delete">
          <span class="trash-icon" aria-hidden="true">🗑</span>
          Delete
        </button>
      </div>
    </td>
    <td>
      <div class="schedule-cell">
        <span class="mono">${safeSchedule}</span>
        <span class="schedule-human">${safeHumanSchedule}</span>
      </div>
    </td>
    <td><code>${safeCommand}</code></td>
  `;

  tr.querySelectorAll("button").forEach((button) => {
    const action = button.dataset.action;
    button.addEventListener("click", async () => {
      if (action === "edit") {
        window.location.href = `/ui/job.html?name=${encodeURIComponent(job.name)}`;
        return;
      }
      if (action === "logs") {
        window.location.href = `/ui/logs.html?name=${encodeURIComponent(job.name)}`;
        return;
      }
      if (action === "run") {
        setStatus(`Triggering ${job.name}...`);
        try {
          await api(`/api/v1/jobs/${encodeURIComponent(job.name)}/run`, { method: "POST" });
          setStatus(`Triggered ${job.name}`);
        } catch (err) {
          setStatus(err.message, true);
        }
        return;
      }
      if (action === "archive") {
        await callAction(
          job.name,
          "archive",
          `Archive job "${job.name}"? It will be removed from active jobs and moved to jobs/archive/.`
        );
        return;
      }
      if (action === "copy") {
        await copyJob(job.name);
        return;
      }
      if (action === "delete") {
        openDeleteModal(job.name);
        return;
      }
      await callAction(job.name, action);
    });
  });

  return tr;
}

async function loadJobs(options = {}) {
  const silent = Boolean(options.silent);
  if (loadInFlight) {
    return;
  }
  loadInFlight = true;

  if (!silent || !hasLoadedOnce) {
    setStatus("Loading jobs...");
  }

  try {
    const jobs = await api("/api/v1/jobs");
    jobs.sort((a, b) => a.name.localeCompare(b.name));

    jobsBodyEl.innerHTML = "";
    if (jobs.length === 0) {
      jobsBodyEl.innerHTML = `<tr><td colspan="6">No jobs found</td></tr>`;
      setStatus("No jobs found");
      hasLoadedOnce = true;
      return;
    }

    jobs.forEach((job) => {
      jobsBodyEl.appendChild(renderJob(job));
    });
    hasLoadedOnce = true;
    if (!silent) {
      if (streamConnected) {
        setStatus(`Loaded ${jobs.length} job(s). Live stream connected.`);
      } else {
        setStatus(`Loaded ${jobs.length} job(s). Waiting for live stream; fallback refresh every ${FALLBACK_POLL_INTERVAL_MS / 1000}s.`);
      }
    }
  } catch (err) {
    jobsBodyEl.innerHTML = `<tr><td colspan="6">Failed to load jobs</td></tr>`;
    setStatus(err.message, true);
  } finally {
    loadInFlight = false;
  }
}

function startPolling() {
  if (pollHandle) {
    clearInterval(pollHandle);
  }

  pollHandle = setInterval(() => {
    if (document.hidden) {
      return;
    }
    loadJobs({ silent: true });
  }, FALLBACK_POLL_INTERVAL_MS);
}

function onRealtimeEvent(event) {
  let payload = null;
  try {
    payload = JSON.parse(event.data);
  } catch (_) {
    payload = null;
  }

  if (payload && payload.type === "run.completed" && payload.job_name) {
    const suffix = payload.status ? ` (${payload.status})` : "";
    setStatus(`Run finished: ${payload.job_name}${suffix}`);
  }

  loadJobs({ silent: true });
}

function startEventStream() {
  if (!("EventSource" in window)) {
    setStatus("Browser does not support EventSource; using fallback refresh.");
    return;
  }

  if (eventStream) {
    eventStream.close();
  }

  eventStream = new EventSource("/api/v1/events");
  eventStream.addEventListener("job.changed", onRealtimeEvent);
  eventStream.addEventListener("run.started", onRealtimeEvent);
  eventStream.addEventListener("run.completed", onRealtimeEvent);
  eventStream.onopen = () => {
    streamConnected = true;
    if (hasLoadedOnce) {
      setStatus("Live stream connected.");
    }
  };
  eventStream.onerror = () => {
    if (streamConnected) {
      setStatus("Live stream disconnected, retrying...");
    }
    streamConnected = false;
  };
}

refreshBtn.addEventListener("click", () => loadJobs());

if (deleteInputEl) {
  deleteInputEl.addEventListener("input", setDeleteConfirmEnabled);
  deleteInputEl.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !deleteConfirmBtn.disabled) {
      event.preventDefault();
      deleteConfirmBtn.click();
    }
  });
}

if (deleteCancelBtn) {
  deleteCancelBtn.addEventListener("click", closeDeleteModal);
}

if (deleteConfirmBtn) {
  deleteConfirmBtn.addEventListener("click", async () => {
    if (!pendingDeleteJobName || deleteConfirmBtn.disabled) {
      return;
    }
    const name = pendingDeleteJobName;
    closeDeleteModal();
    await callAction(name, "");
  });
}

if (deleteModalEl) {
  deleteModalEl.addEventListener("click", (event) => {
    if (event.target === deleteModalEl) {
      closeDeleteModal();
    }
  });
}

document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    loadJobs({ silent: true });
  }
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && deleteModalEl && deleteModalEl.classList.contains("open")) {
    closeDeleteModal();
  }
});

loadJobs().then(() => {
  startEventStream();
  startPolling();
});

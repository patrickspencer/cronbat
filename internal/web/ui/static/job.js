const params = new URLSearchParams(window.location.search);
let jobName = params.get("name");

const titleEl = document.getElementById("job-title");
const logsLinkEl = document.getElementById("job-logs-link");
const statusEl = document.getElementById("status");

const formEl = document.getElementById("settings-form");
const nameEl = document.getElementById("name");
const scheduleEl = document.getElementById("schedule");
const commandEl = document.getElementById("command");
const workingDirEl = document.getElementById("working-dir");
const executorEl = document.getElementById("executor");
const timeoutEl = document.getElementById("timeout");
const enabledEl = document.getElementById("enabled");
const envEl = document.getElementById("env");
const onSuccessEl = document.getElementById("on-success");
const onFailureEl = document.getElementById("on-failure");
const metadataEl = document.getElementById("metadata");
const yamlEl = document.getElementById("yaml");

const reloadSettingsBtn = document.getElementById("reload-settings");
const saveYamlBtn = document.getElementById("save-yaml");
const reloadYamlBtn = document.getElementById("reload-yaml");
const sideStateEl = document.getElementById("side-state");
const sideNextRunEl = document.getElementById("side-next-run");
const sideWorkingDirEl = document.getElementById("side-working-dir");
const sideExecutorEl = document.getElementById("side-executor");
const sideLogsLinkEl = document.getElementById("side-logs-link");
const sideCopyBtn = document.getElementById("side-copy-btn");
const sideDeleteBtn = document.getElementById("side-delete-btn");
const deleteModalEl = document.getElementById("delete-modal");
const deleteJobNameEl = document.getElementById("delete-job-name");
const deleteInputEl = document.getElementById("delete-confirm-input");
const deleteConfirmBtn = document.getElementById("delete-confirm-btn");
const deleteCancelBtn = document.getElementById("delete-cancel-btn");

function refreshNavLinks() {
  const logsURL = `/ui/logs.html?name=${encodeURIComponent(jobName)}`;
  logsLinkEl.href = logsURL;
  sideLogsLinkEl.href = logsURL;
}

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

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `request failed (${response.status})`);
  }
  return payload;
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
  if (deleteInputEl) {
    deleteInputEl.value = "";
  }
  setDeleteConfirmEnabled();
}

function openDeleteModal() {
  if (!deleteModalEl || !deleteJobNameEl || !deleteInputEl) {
    return;
  }
  deleteJobNameEl.textContent = jobName;
  deleteInputEl.value = "";
  setDeleteConfirmEnabled();
  deleteModalEl.classList.add("open");
  deleteModalEl.setAttribute("aria-hidden", "false");
  deleteInputEl.focus();
}

function parseKeyValueLines(input) {
  const out = {};
  const lines = input.split("\n");
  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) {
      continue;
    }
    const idx = line.indexOf("=");
    if (idx <= 0) {
      throw new Error(`Invalid env line: ${line}`);
    }
    const key = line.slice(0, idx).trim();
    const value = line.slice(idx + 1);
    out[key] = value;
  }
  return out;
}

function splitLines(list) {
  if (!Array.isArray(list)) {
    return "";
  }
  return list.join("\n");
}

function stringifyEnv(env) {
  if (!env || typeof env !== "object") {
    return "";
  }
  return Object.entries(env)
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

async function loadSettings(options = {}) {
  if (!options.silent) {
    setStatus("Loading settings...");
  }
  const job = await api(`/api/v1/jobs/${encodeURIComponent(jobName)}`);
  titleEl.textContent = `Job: ${job.name}`;

  nameEl.value = job.name || "";
  scheduleEl.value = job.schedule || "";
  commandEl.value = job.command || "";
  workingDirEl.value = job.working_dir || "";
  executorEl.value = job.executor || "";
  timeoutEl.value = job.timeout || "";
  enabledEl.checked = Boolean(job.enabled);
  envEl.value = stringifyEnv(job.env);
  onSuccessEl.value = splitLines(job.on_success);
  onFailureEl.value = splitLines(job.on_failure);
  metadataEl.value = JSON.stringify(job.metadata || {}, null, 2);

  sideStateEl.textContent = (job.state || (job.enabled ? "started" : "stopped")).toLowerCase();
  sideNextRunEl.textContent = formatDate(job.next_run);
  sideWorkingDirEl.textContent = job.working_dir || "(server working dir)";
  sideExecutorEl.textContent = job.executor || "shell";
}

async function loadYAML(options = {}) {
  if (!options.silent) {
    setStatus("Loading YAML...");
  }
  const payload = await api(`/api/v1/jobs/${encodeURIComponent(jobName)}/yaml`);
  yamlEl.value = payload.yaml || "";
}

formEl.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("Saving settings...");

  try {
    let metadata = {};
    const metadataInput = metadataEl.value.trim();
    if (metadataInput !== "") {
      metadata = JSON.parse(metadataInput);
      if (typeof metadata !== "object" || Array.isArray(metadata) || metadata === null) {
        throw new Error("Metadata must be a JSON object");
      }
    }

    const payload = {
      name: nameEl.value.trim(),
      schedule: scheduleEl.value.trim(),
      command: commandEl.value,
      working_dir: workingDirEl.value.trim(),
      executor: executorEl.value.trim(),
      timeout: timeoutEl.value.trim(),
      enabled: enabledEl.checked,
      env: parseKeyValueLines(envEl.value),
      on_success: onSuccessEl.value.split("\n").map((v) => v.trim()).filter(Boolean),
      on_failure: onFailureEl.value.split("\n").map((v) => v.trim()).filter(Boolean),
      metadata
    };

    await api(`/api/v1/jobs/${encodeURIComponent(jobName)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });

    await loadYAML({ silent: true });
    setStatus("Settings saved");
  } catch (err) {
    setStatus(err.message, true);
  }
});

saveYamlBtn.addEventListener("click", async () => {
  setStatus("Saving YAML...");
  try {
    const payload = await api(`/api/v1/jobs/${encodeURIComponent(jobName)}/yaml`, {
      method: "PUT",
      headers: { "Content-Type": "text/plain" },
      body: yamlEl.value
    });
    let successMessage = "YAML saved";
    if (payload.name && payload.name !== jobName) {
      jobName = payload.name;
      const nextURL = `/ui/job.html?name=${encodeURIComponent(jobName)}`;
      window.history.replaceState(null, "", nextURL);
      refreshNavLinks();
      successMessage = `YAML saved. Job renamed to ${jobName}`;
    }
    await loadSettings({ silent: true });
    await loadYAML({ silent: true });
    setStatus(successMessage);
  } catch (err) {
    setStatus(err.message, true);
  }
});

reloadSettingsBtn.addEventListener("click", async () => {
  try {
    await loadSettings();
    setStatus("Settings reloaded");
  } catch (err) {
    setStatus(err.message, true);
  }
});

reloadYamlBtn.addEventListener("click", async () => {
  try {
    await loadYAML();
    setStatus("YAML reloaded");
  } catch (err) {
    setStatus(err.message, true);
  }
});

if (sideCopyBtn) {
  sideCopyBtn.addEventListener("click", async () => {
    const defaultName = `${jobName}_copy`;
    const input = window.prompt("Name for copied job:", defaultName);
    if (input === null) {
      return;
    }
    const newName = input.trim();
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
      window.location.href = `/ui/job.html?name=${encodeURIComponent(newName)}`;
    } catch (err) {
      setStatus(err.message, true);
    }
  });
}

if (sideDeleteBtn) {
  sideDeleteBtn.addEventListener("click", () => {
    openDeleteModal();
  });
}

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
    if (deleteConfirmBtn.disabled) {
      return;
    }
    closeDeleteModal();
    setStatus(`Deleting ${jobName}...`);
    try {
      await api(`/api/v1/jobs/${encodeURIComponent(jobName)}`, { method: "DELETE" });
      window.location.href = "/ui/";
    } catch (err) {
      setStatus(err.message, true);
    }
  });
}

if (deleteModalEl) {
  deleteModalEl.addEventListener("click", (event) => {
    if (event.target === deleteModalEl) {
      closeDeleteModal();
    }
  });
}

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && deleteModalEl && deleteModalEl.classList.contains("open")) {
    closeDeleteModal();
  }
});

async function init() {
  if (!jobName) {
    setStatus("Missing job name in URL query (?name=...)", true);
    formEl.querySelectorAll("input, textarea, button").forEach((el) => {
      el.disabled = true;
    });
    return;
  }

  refreshNavLinks();

  try {
    await loadSettings();
    await loadYAML();
    setStatus(`Editing ${jobName}`);
  } catch (err) {
    setStatus(err.message, true);
  }
}

init();

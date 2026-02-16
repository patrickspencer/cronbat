const formEl = document.getElementById("new-job-form");
const statusEl = document.getElementById("status");

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

function setStatus(message, isError = false) {
  statusEl.textContent = message;
  statusEl.classList.toggle("error", isError);
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

async function api(path, options = {}) {
  const response = await fetch(path, options);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `request failed (${response.status})`);
  }
  return payload;
}

formEl.addEventListener("submit", async (event) => {
  event.preventDefault();
  setStatus("Creating job...");

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
      command: commandEl.value.trim(),
      working_dir: workingDirEl.value.trim(),
      executor: executorEl.value.trim(),
      timeout: timeoutEl.value.trim(),
      enabled: enabledEl.checked,
      env: parseKeyValueLines(envEl.value),
      on_success: onSuccessEl.value.split("\n").map((v) => v.trim()).filter(Boolean),
      on_failure: onFailureEl.value.split("\n").map((v) => v.trim()).filter(Boolean),
      metadata
    };

    const result = await api("/api/v1/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });

    setStatus(`Created ${result.name}`);
    window.location.href = `/ui/job.html?name=${encodeURIComponent(result.name)}`;
  } catch (err) {
    setStatus(err.message, true);
  }
});

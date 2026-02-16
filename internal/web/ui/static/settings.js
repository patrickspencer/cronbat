const statusEl = document.getElementById("status");
const statsEl = document.getElementById("stats");
const configEl = document.getElementById("config");

function setStatus(message, isError = false) {
  statusEl.textContent = message;
  statusEl.classList.toggle("error", isError);
}

async function api(path) {
  const response = await fetch(path);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || `request failed (${response.status})`);
  }
  return payload;
}

async function load() {
  setStatus("Loading settings...");
  try {
    const [health, stats, config] = await Promise.all([
      api("/api/v1/health"),
      api("/api/v1/stats"),
      api("/api/v1/config")
    ]);

    statsEl.textContent = JSON.stringify({ health, stats }, null, 2);
    configEl.textContent = JSON.stringify(config, null, 2);
    setStatus("Loaded settings");
  } catch (err) {
    setStatus(err.message, true);
  }
}

load();

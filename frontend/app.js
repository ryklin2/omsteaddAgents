const API_URL = import.meta.env?.VITE_API_URL || "http://localhost:8080";

const AGENTS = {
  "mcdonalds-receipt-surveyer": {
    name: "McDonald's Receipt Surveyer",
    description:
      "Automates McDonald's McDVoice receipt survey entries and generates your validation code.",
    fields: [
      {
        name: "survey_code",
        label: "26-Digit Receipt Survey Code",
        placeholder: "12345-67890-12345-67890-12345-6",
        required: true,
      },
      {
        name: "visit_type",
        label: "Visit Type",
        type: "select",
        default: "Drive-Thru",
        options: [
          { value: "Drive-Thru", label: "Drive-Thru" },
          { value: "Dine-In", label: "Dine-In" },
          { value: "Carry-Out", label: "Carry-Out" },
        ],
      },
      {
        name: "rating",
        label: "Overall Satisfaction",
        type: "select",
        default: "5",
        options: [
          { value: "5", label: "5 - Highly Satisfied" },
          { value: "4", label: "4 - Satisfied" },
          { value: "3", label: "3 - Neither" },
          { value: "2", label: "2 - Dissatisfied" },
          { value: "1", label: "1 - Highly Dissatisfied" },
        ],
      },
      {
        name: "comment",
        label: "Comment (optional, submitted verbatim)",
        placeholder: "Friendly crew and my order was hot and correct.",
        type: "textarea",
      },
    ],
  },
  "warcraftlogs-blame-me": {
    name: "Warcraftlogs Blame Me",
    description:
      "Analyzes a Warcraft Logs report URL or code to pinpoint avoidable damage, missed interrupts, and wipe causes.",
    fields: [
      {
        name: "report_url",
        label: "Warcraft Logs Report URL or Code",
        placeholder: "https://www.warcraftlogs.com/reports/aBcDeFg123456789",
        required: true,
      },
      {
        name: "player_name",
        label: "Character Name (optional)",
        placeholder: "Thrall",
        required: false,
      },
    ],
  },
  "git-repo-code-reviewer": {
    name: "Git Repo Code Reviewer",
    description:
      "Reviews a public Git repository for architecture, security, and maintainability issues.",
    fields: [
      {
        name: "repo_url",
        label: "Git Repository URL",
        placeholder: "https://github.com/ryklin2/omsteaddAgents",
        required: true,
      },
      {
        name: "focus_area",
        label: "Review Focus (optional)",
        placeholder: "security, concurrency, sql",
        required: false,
      },
    ],
  },
};

// The API's catalog (GET /api/agents) is the source of truth for each agent's
// form, so a field added in backend/agents/agents.go shows up here without a
// frontend change. AGENTS above is the fallback for when the API is down.
async function loadCatalog() {
  try {
    // Bounded, because the page waits on this before its first render and a
    // cold Cloud Run start can take seconds; past that, the fallback is used.
    const res = await fetch(`${API_URL}/api/agents`, {
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(3000),
    });
    if (!res.ok) return;
    const data = await res.json();
    for (const a of data.agents || []) {
      AGENTS[a.slug] = {
        name: a.name,
        description: a.description,
        fields: a.fields || [],
      };
    }
  } catch {
    // Keep the built-in catalog; checkApiHealth reports the outage.
  }
}

function renderField(field) {
  const id = `field-${escapeHtml(field.name)}`;
  const name = escapeHtml(field.name);
  const required = field.required ? "required" : "";
  const def = field.default ?? "";
  let input;
  if (field.type === "select") {
    input = `
      <select id="${id}" name="${name}" ${required}>
        ${(field.options || [])
          .map(
            (o) => `<option value="${escapeHtml(o.value)}" ${o.value === def ? "selected" : ""}>${escapeHtml(o.label)}</option>`
          )
          .join("")}
      </select>`;
  } else if (field.type === "textarea") {
    input = `<textarea id="${id}" name="${name}" rows="3" placeholder="${escapeHtml(field.placeholder)}" ${required}>${escapeHtml(def)}</textarea>`;
  } else {
    input = `<input id="${id}" name="${name}" type="text" value="${escapeHtml(def)}" placeholder="${escapeHtml(field.placeholder)}" ${required} />`;
  }
  return `
    <div class="form-field">
      <label for="${id}">${escapeHtml(field.label)}</label>
      ${input}
    </div>`;
}

// renderResult shows the facts a run produced up front. For the McDonald's
// agent that is the validation code and whether it is real or simulated; the
// full payload stays available underneath for debugging.
function renderResult(data) {
  const out = data.output_payload || {};
  let headline = "";
  if (out.validation_code) {
    headline = `
      <div class="validation-card" data-mode="${escapeHtml(out.mode)}">
        <span class="validation-label">Validation code${out.mode === "simulation" ? " (simulated)" : ""}</span>
        <span class="validation-code">${escapeHtml(out.validation_code)}</span>
        <span class="muted">Submitted as ${escapeHtml(out.rating)}/5, ${escapeHtml(out.visit_type)}</span>
      </div>`;
  } else if (out.invalid_reason) {
    headline = `<p class="error-text">${escapeHtml(out.invalid_reason)}</p>`;
  }
  const notice = out.notice ? `<p class="notice">${escapeHtml(out.notice)}</p>` : "";
  const summary = out.summary ? `<p>${escapeHtml(out.summary)}</p>` : "";
  const error = data.error_message ? `<p class="error-text">${escapeHtml(data.error_message)}</p>` : "";
  return `
    <strong>Run ${escapeHtml(data.status)}</strong> (ID: <code>${escapeHtml(data.id)}</code>)
    ${headline}${notice}${error}${summary}
    <details><summary>Full output</summary>
      <pre>${escapeHtml(JSON.stringify(out, null, 2))}</pre>
    </details>
  `;
}

async function checkApiHealth() {
  const statusEl = document.getElementById("api-status");
  if (!statusEl) return;

  try {
    const response = await fetch(`${API_URL}/health`, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const data = await response.json();
    statusEl.textContent = `API status: ${data.status} (DB: ${data.db})`;
    statusEl.dataset.state = "ok";
  } catch (err) {
    statusEl.textContent = `API unreachable at ${API_URL}`;
    statusEl.dataset.state = "error";
  }
}

function renderHome(root) {
  document.title = "Ryklin's Agents";
  root.innerHTML = `
    <ul class="agent-list">
      ${Object.entries(AGENTS)
        .map(
          ([slug, meta]) => `
        <li class="agent-card">
          <a href="/${slug}" data-nav class="agent-link">${escapeHtml(meta.name)}</a>
          <p class="agent-desc">${escapeHtml(meta.description)}</p>
        </li>`
        )
        .join("")}
    </ul>
  `;
}

async function renderAgentTool(root, slug) {
  const meta = AGENTS[slug];
  if (!meta) {
    document.title = "Not Found — Ryklin's Agents";
    root.innerHTML = `
      <p>Unknown tool route.</p>
      <p><a href="/" data-nav>&larr; Back to all agents</a></p>
    `;
    return;
  }

  document.title = `${meta.name} — Ryklin's Agents`;
  root.innerHTML = `
    <nav class="breadcrumb"><a href="/" data-nav>&larr; All Agents</a></nav>
    <section class="tool-section">
      <h2>${escapeHtml(meta.name)}</h2>
      <p>${escapeHtml(meta.description)}</p>

      <form id="agent-form" class="agent-form">
        ${meta.fields.map(renderField).join("")}
        <button type="submit" id="submit-btn">Run Agent</button>
      </form>

      <div id="run-output" class="run-output" hidden></div>
    </section>

    <section class="history-section">
      <h3>Recent Runs</h3>
      <div id="runs-list" class="runs-list">Loading recent runs...</div>
    </section>
  `;

  const form = document.getElementById("agent-form");
  const outputEl = document.getElementById("run-output");
  const submitBtn = document.getElementById("submit-btn");

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const formData = new FormData(form);
    const payload = Object.fromEntries(formData.entries());

    submitBtn.disabled = true;
    submitBtn.textContent = "Running...";
    outputEl.hidden = false;
    outputEl.textContent = "Submitting agent run...";

    try {
      const res = await fetch(`${API_URL}/api/agents/${slug}/run`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify(payload),
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      outputEl.innerHTML = renderResult(data);
      await loadRecentRuns(slug);
    } catch (err) {
      outputEl.innerHTML = `<span class="error-text">Error: ${escapeHtml(err.message)}</span>`;
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = "Run Agent";
    }
  });

  await loadRecentRuns(slug);
}

async function loadRecentRuns(slug) {
  const listEl = document.getElementById("runs-list");
  if (!listEl) return;

  try {
    const res = await fetch(`${API_URL}/api/agents/${slug}/runs`, {
      headers: { Accept: "application/json" },
    });
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}`);
    }
    const data = await res.json();
    const runs = data.runs || [];
    if (runs.length === 0) {
      listEl.innerHTML = `<p class="muted">No runs recorded yet.</p>`;
      return;
    }
    listEl.innerHTML = runs
      .map(
        (r) => `
        <article class="run-item">
          <div class="run-item-header">
            <span class="run-status" data-status="${escapeHtml(r.status)}">${escapeHtml(r.status)}</span>
            <time datetime="${escapeHtml(r.created_at)}">${new Date(r.created_at).toLocaleString()}</time>
          </div>
          <pre>${escapeHtml(JSON.stringify(r.output_payload, null, 2))}</pre>
        </article>`
      )
      .join("");
  } catch (err) {
    listEl.innerHTML = `<p class="muted">Could not fetch run history (${escapeHtml(err.message)}).</p>`;
  }
}

function escapeHtml(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function handleRoute() {
  const root = document.getElementById("app-root");
  if (!root) return;
  const path = window.location.pathname.replace(/^\/+|\/+$/g, "");
  if (!path) {
    renderHome(root);
  } else {
    renderAgentTool(root, path);
  }
}

document.addEventListener("click", (e) => {
  const link = e.target.closest("a[data-nav]");
  if (!link) return;
  const url = new URL(link.href, window.location.origin);
  if (url.origin !== window.location.origin) return;
  e.preventDefault();
  if (url.pathname !== window.location.pathname) {
    window.history.pushState({}, "", url.pathname);
    handleRoute();
  }
});

window.addEventListener("popstate", handleRoute);

checkApiHealth();
loadCatalog().then(handleRoute);

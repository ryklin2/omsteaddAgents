# Learning Google ADK (Go) by Building Ryklin's Agents

This guide turns `omsteaddAgents` into a hands-on learning project for **Google's Agent Development Kit (ADK) for Go** and the **Gemini API**.

Instead of having an AI write all the agent code for you, the HTTP server (`backend/cmd/api/main.go`) and frontend (`frontend/`) are already wired up as your test harness, while the agent files in `backend/agents/` contain guided **`TODO(learn)`** milestones that you can complete step-by-step alongside the official ADK docs.

---

## Official Reference Links (Bookmark These)

- **ADK Documentation Hub**: https://adk.dev/ (Go tab)
- **ADK LLM Context File** (great to paste into LLMs when asking questions): https://adk.dev/llms.txt and https://adk.dev/llms-full.txt
- **Go Package Docs (`pkg.go.dev`)**:
  - Core Agent & LLM Agent: https://pkg.go.dev/google.golang.org/adk/agent/llmagent
  - Gemini Model Wrapper: https://pkg.go.dev/google.golang.org/adk/model/gemini
  - Custom Function Tools: https://pkg.go.dev/google.golang.org/adk/tool/functiontool
  - Runner & Sessions: https://pkg.go.dev/google.golang.org/adk/runner and https://pkg.go.dev/google.golang.org/adk/session
  - Google GenAI Go SDK: https://pkg.go.dev/google.golang.org/genai
- **Google AI Studio (Free Gemini API Key)**: https://aistudio.google.com/apikey

---

## How to Use LLMs as a Tutor (Without Spoiling the Code)

When you're working on a `TODO(learn)` block and want help from Gemini/ChatGPT/Claude or Antigravity, use prompts like:

> *"I'm learning Google ADK for Go (`google.golang.org/adk`). Don't write the full solution for me—explain how `functiontool.New` infers the JSON schema from my Go struct, and review my struct definition in `backend/agents/mcdonalds_lab.go`."*

---

## Module 0: Local Setup & ADK Playground

Before touching the web server or database, get a standalone ADK agent running in your terminal.

### 1. Install Go & Get a Gemini API Key
1. Install Go (1.25+ recommended by ADK) and verify with `go version`.
2. Create a free API key at https://aistudio.google.com/apikey and export it in your terminal:
   ```bash
   export GOOGLE_API_KEY="your-api-key-here"
   ```

### 2. Install the ADK & GenAI Go Packages
Inside `backend/`:
```bash
cd backend
go get google.golang.org/adk@latest
go get google.golang.org/genai@latest
go mod tidy
```

### 3. Complete Your First Exercise in `backend/cmd/adk-playground/main.go`
Open [`backend/cmd/adk-playground/main.go`](../backend/cmd/adk-playground/main.go). Follow the `TODO(learn)` comments to:
1. Initialize `gemini.NewModel(ctx, "gemini-3.5-flash", &genai.ClientConfig{APIKey: ...})`

   > **Model availability:** `gemini-2.5-flash` is retired for newly created API keys (it 404s with
   > "no longer available to new users"). The newest Flash models (`gemini-3.8-flash`,
   > `gemini-3.7-flash`, `gemini-flash-latest`) are frequently capacity-limited and return
   > `503 UNAVAILABLE`. `gemini-3.5-flash` is the current reliable default. To see what your own key
   > can reach:
   >
   > ```bash
   > curl -s "https://generativelanguage.googleapis.com/v1beta/models?key=$GOOGLE_API_KEY" \
   >   | grep -o '"name": "models/[^"]*"'
   > ```
   >
   > A listed model can still 404 or 503 — the list is not a guarantee of access.
2. Create an `llmagent.New(llmagent.Config{...})`
3. Run it from your terminal using ADK's built-in `full.NewLauncher()` (which gives you both a CLI chat loop and ADK's built-in local web debugger!).

---

## Module 1: Custom Function Tools (`McDonald's Receipt Surveyer`)

**File**: [`backend/agents/mcdonalds_lab.go`](../backend/agents/mcdonalds_lab.go)

**What you'll learn**:
1. **Typed Tool Inputs & Outputs**: How ADK uses Go structs and struct tags (`json:"..." jsonschema:"..."`) to tell Gemini what parameters a tool expects.
2. **Wrapping Go Functions with `functiontool.New`**: Turning normal Go code (like validating a 26-digit receipt code or submitting an HTTP form to `mcdvoice.com`) into a `tool.Tool` that Gemini can invoke.
3. **System Instructions (`Instruction`)**: Prompting the agent so it validates the receipt code first, calls your survey tool, and returns a clean validation code.

**Your Checklist**:
- [x] **Step 1.1**: Define the `ParseReceiptCodeArgs` and `ParseReceiptCodeResult` structs with `json` and `jsonschema` tags.
- [x] **Step 1.2**: Implement `parseReceiptCode` and wrap it with `functiontool.New`.
- [x] **Step 1.3**: Define a second tool (`submit_mcdvoice_survey`) that walks through the survey and returns the validation code.
- [x] **Step 1.4**: Wire both tools into `llmagent.New` and execute the agent with `runner.New`.

### API corrections found while building this

The handler signature in this guide was wrong. ADK v1.9.0 requires:

```go
func(agent.ToolContext, TArgs) (TResults, error)   // not tool.Context
```

`agent.ToolContext` embeds `context.Context` (through `agent.ReadonlyContext`), so it can be
passed directly to `http.NewRequestWithContext` — no separate context parameter is needed, and
cancelling the agent run cancels any in-flight tool I/O.

Schema inference comes from `github.com/google/jsonschema-go`, where the `jsonschema:"..."` tag
value becomes the property **description**. Two things that will bite you:

- An **empty** `jsonschema:""` tag is a hard error, not a missing description.
- Tag values may not begin with `WORD=`; that form is reserved.

### Error channel: `error` vs. a result field

A tool returning a non-nil `error` tells the model *the tool broke*, which usually triggers a retry
or an abandoned run. That is the wrong channel for an expected outcome like "this code is only 8
digits" — the model should explain that to the user, not retry it. So:

| Situation | Channel |
|---|---|
| User's code is malformed | `Valid: false` + `Reason` on a successful return |
| Model passed args that contradict a previous tool result | `error` |
| Network/dependency failure | `error` |

### Live vs. simulated submission

`submit_mcdvoice_survey` has two modes, selected by `MCDVOICE_LIVE`:

```bash
# Simulation (default): no network call, deterministic placeholder code.
go run ./cmd/api

# Live: really completes the survey and returns the real validation code.
export MCDVOICE_LIVE=1
export MCDVOICE_RATING=5     # your standing satisfaction rating, 1-5
export MCDVOICE_DEBUG=1      # dump each page's controls while iterating
go run ./cmd/api
```

Live is opt-in because each run is irreversible and consumes the single use of a real receipt code
— keep it off while developing the agent, or you will burn receipts on debugging.

The entry form (`CN1`-`CN6`, `JavaScriptEnabled`, `FIP`, `AllowCapture`, `NextButton`) was read
from the live site and is covered by a fixture test. The question pages after it have **not** been
observed, so `answerPage` in `backend/agents/mcdvoice_live.go` walks them generically: it parses
whatever form each page serves and picks radio options by matching label text against a
most-positive-first keyword list. Expect to tune it once you have a real transcript.

---

## Module 2: External APIs & Reasoning Loops (`Warcraftlogs Blame Me`)

**File**: [`backend/agents/warcraftlogs_lab.go`](../backend/agents/warcraftlogs_lab.go)

**What you'll learn**:
1. **Multi-Tool Reasoning**: Giving an `llmagent` multiple tools (`extract_report_code`, `fetch_wipe_summary`, `fetch_player_avoidable_damage`) so Gemini decides which tools to call and in what order based on the user's question.
2. **Structured Output / Grounded Explanations**: Instructing the agent to cite concrete timestamps and spell names from tool outputs instead of hallucinating raid mechanics.

**Your Checklist**:
- [ ] **Step 2.1**: Write a tool that parses a Warcraft Logs URL (`https://www.warcraftlogs.com/reports/aBcDeFg123456789#fight=3`) into `reportCode` and `fightID`.
- [ ] **Step 2.2**: Write a tool that queries the Warcraft Logs v2 GraphQL API (or returns mock combat events first while testing) for deaths, interrupts, and avoidable damage.
- [ ] **Step 2.3**: Configure the `llmagent` instruction to synthesize a playful but accurate "Blame Report".

---

## Module 3: Multi-Agent Workflows (`Git Repo Code Reviewer`)

**File**: [`backend/agents/code_reviewer_lab.go`](../backend/agents/code_reviewer_lab.go)

**What you'll learn**:
1. **Sub-Agents & Orchestration**: In ADK, an agent isn't limited to a single prompt. You can compose specialized sub-agents (e.g. using `sequentialagent` or `SubAgents` in `llmagent.Config`):
   - **Agent A (`repo_fetcher`)**: Uses GitHub API tools to fetch the repository file tree and key source files.
   - **Agent B (`security_and_style_critic`)**: Reviews the fetched files against your focus area (`security`, `concurrency`, `sql`) and produces structured findings.

**Your Checklist**:
- [ ] **Step 3.1**: Build a `fetch_github_repo_tree` function tool using Go's `net/http` against `https://api.github.com/repos/{owner}/{repo}/git/trees/HEAD?recursive=1`.
- [ ] **Step 3.2**: Build a `fetch_github_file` function tool to read specific files the agent wants to inspect.
- [ ] **Step 3.3**: Combine a reader agent and a reviewer agent using ADK's multi-agent workflow primitives.

---

## Module 4: Connecting ADK `runner.Runner` to the Web UI

Once any of your agents work in `cmd/adk-playground`, [`backend/agents/agents.go`](../backend/agents/agents.go) dispatches `POST /api/agents/{slug}/run` directly to your lab functions (`RunMcDonaldsAgent`, `RunWarcraftLogsAgent`, `RunCodeReviewerAgent`).

Run the backend and frontend locally to see your ADK agent power the web UI:
```bash
# Terminal 1: Start the Go API (works in-memory even without Docker/Postgres!)
cd backend
export GOOGLE_API_KEY="your-api-key"
go run ./cmd/api

# Terminal 2: Start the Vite frontend on http://localhost:5173
cd frontend
npm install
npm run dev
```


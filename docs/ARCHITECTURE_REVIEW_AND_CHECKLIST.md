# Architecture Review & Implementation Plan: `omsteaddAgents` (Ryklin's Agents)

## 1. Executive Architecture Review

Your stack—**Cloudflare Pages** (frontend), **GCP Cloud Run with Go** (backend API), and **Databricks Lakebase / Neon Serverless Postgres** (database)—is a pragmatic, low-cost architecture rather than bloated "resume-driven development." Each service is paired with the workload where its free tier and runtime profile shine:

| Layer | Technology Choice | Why It Works on a \$0 Budget | Key Operational Trade-Off | 
| ----- | ----- | ----- | ----- | 
| **Frontend** | [Cloudflare Pages](https://pages.cloudflare.com/) | Unlimited static bandwidth, global edge caching, automatic Git branch preview builds | Build config in `DEPLOYMENT_PLAN.md` assumes Vite (`npm run build` -> `dist`), whereas `frontend/` is currently buildless vanilla HTML/JS/CSS | 
| **Backend API** | [Google Cloud Run](https://cloud.google.com/run) (Go + `distroless`) | Go compiles to a static binary (\~10–15 MB) with \~50–150 ms cold starts, making `--min-instances 0` viable | Cloud Build leaves old container images in Artifact Registry, which exceeds the 500 MB free tier if no cleanup policy is set | 
| **Database** | [Databricks Lakebase](https://www.databricks.com/product/lakebase) / [Neon Postgres](https://neon.com/docs/introduction/neon-and-lakebase) | Scale-to-zero serverless Postgres, built-in PgBouncer pooling (`-pooler`), instant copy-on-write branching | Runs on AWS (`aws-us-west-2`), so every query from GCP Cloud Run (`us-west1`) crosses clouds over TLS (\~2–5 ms RTT within Oregon) | 

### Critical Mismatches Found in the Repository

Comparing `README.md` and `DEPLOYMENT_PLAN.md` against the actual files in your repository surfaced six concrete blockers that will break Phase 1–4 if not resolved first:

1. **Migration Path Mismatch (`db/` vs. `backend/migrations/`)**:

   * `README.md` and `DEPLOYMENT_PLAN.md` run `goose -dir backend/migrations ...`, but your SQL file actually lives at `db/00001_init.sql`.

   * **Fix**: Move `db/00001_init.sql` into `backend/migrations/00001_init.sql` (recommended so `backend/` is self-contained) or change all `goose -dir` commands to `db`.

2. **Broken Documentation Path (`DEPLOYMENT_PLAN.md` vs. `docs/DEPLOYMENT_PLAN.md`)**:

   * `README.md` links to `docs/DEPLOYMENT_PLAN.md` (lines 51, 64, 120), but `DEPLOYMENT_PLAN.md` is at the repository root.

   * **Fix**: Either move `DEPLOYMENT_PLAN.md` into `docs/DEPLOYMENT_PLAN.md` or update the links in `README.md`.

3. **Frontend Build Pipeline Mismatch (Vanilla Static Files vs. Vite)**:

   * `frontend/` currently has only `index.html`, `app.js`, and `styles.css`—there is no `package.json` or Vite setup.

   * If you configure Cloudflare Pages with `npm run build` and output directory `dist` (Phase 4), the deployment will fail immediately.

   * **Fix**: Initialize a minimal Vite project (`package.json`) in `frontend/` so `import.meta.env.VITE_API_URL` and local dev (`localhost:5173`) work as documented, or remove the build command in Cloudflare Pages and serve the static directory directly.

4. **`.gitignore` Silently Ignores `.yaml` Files**:

   * Line 34 of `.gitignore` contains `.yaml`, which will block committing `.github/workflows/*.yaml` or config files ending in `.yaml`. Meanwhile, `.env.local` (referenced in `README.md` line 72) is not ignored because `.gitignore` only lists `.env`.

   * **Fix**: Replace `.yaml` with `.env*` (and `!.env.example`) plus `frontend/node_modules/` and `frontend/dist/`.

5. **Missing Backend & Compose Scaffolding**:

   * `backend/` (`go.mod`, `cmd/api/main.go`, `Dockerfile`, `.dockerignore`) and `docker-compose.yml` are documented in `DEPLOYMENT_PLAN.md` Phase 1 but do not exist in the repository yet.

6. **Domain Schema vs. Actual Agent Tools**:

   * `frontend/index.html` defines three tools for **Ryklin's Agents**:

     * `/mcdonalds-receipt-surveyer`

     * `/warcraftlogs-blame-me`

     * `/git-repo-code-reviewer`

   * `db/00001_init.sql` currently has `users`, `sessions`, and a placeholder `projects` table. You will need a migration (`00002_agent_runs.sql`) to replace or supplement `projects` with tables that track agent tool executions, inputs, statuses, and structured outputs.

### Architectural Recommendations to Keep It Cheap & Fast

* **Avoid N+1 Queries Across the GCP-to-AWS Hop**: Because Cloud Run (`us-west1`) talks to Lakebase/Neon (`aws-us-west-2`) across cloud providers, keep API endpoints to 1–2 SQL round-trips per request using CTEs or joins.

* **Handle Double Scale-to-Zero Timeouts Gracefully**: When both Cloud Run and Lakebase/Neon wake from idle, the initial connection can take 500–1,000 ms. Set `connect_timeout=10` on the `DATABASE_URL` and configure `pgxpool` with `MaxConns: 5` so up to 3 Cloud Run instances never exceed 15 pooled connections.

* **Simplify Cookie Auth & CORS with a Cloudflare Proxy Rule**: If you use the `sessions` table (`00001_init.sql`) with cookies, browser third-party cookie blocking will break login between `*.pages.dev` and `*.run.app`. Routing `/api/*` through a Cloudflare Pages `_redirects` / Worker proxy makes API requests same-origin from day one, eliminating CORS preflight latency and Safari cookie issues even before setting up a custom domain.

## 2. Measurable Task Breakdown Summary

| Task ID | Phase | Task Name | Target Files / Systems | Measurable Acceptance Metric (Definition of Done) | 
| ----- | ----- | ----- | ----- | ----- | 
| **TASK-01** | Phase 0 | Reconcile Repo Paths & `.gitignore` | `.gitignore`, `README.md`, `backend/migrations/00001_init.sql` | `git status` ignores `.env.local` & `dist/`; `ls backend/migrations/00001_init.sql` succeeds; no broken links in `README.md` | 
| **TASK-02** | Phase 0 | GCP Project, Budget Alerts & CLI Setup | GCP Console, `gcloud` CLI | Budget alerts active at \$1 and $10; `gcloud config list` shows `PROJECT_ID` and `us-west1`; 4 core APIs enabled | 
| **TASK-03** | Phase 0 | Local Toolchain Installation | Local workstation (`go`, `docker`, `node`, `goose`) | `go version`, `docker compose version`, `node -v`, and `goose -version` all exit 0 | 
| **TASK-04** | Phase 1 | Scaffold Go API Server (`cmd/api`) | `backend/go.mod`, `backend/cmd/api/main.go` | Server binds `:$PORT`, handles `/health` (200 OK), answers `OPTIONS` CORS preflight, and gracefully drains on `SIGTERM` | 
| **TASK-05** | Phase 1 | Containerize Backend & Compose Stack | `backend/Dockerfile`, `backend/.dockerignore`, `docker-compose.yml` | `docker compose up -d db && docker compose up --build api` starts cleanly; image size `< 25 MB` | 
| **TASK-06** | Phase 1 | Verify Local Migrations & DB Health | `backend/migrations/00001_init.sql`, Local Postgres | `goose status` shows `00001_init.sql` applied; `curl -i http://localhost:8080/health` returns `HTTP/1.1 200 OK` | 
| **TASK-07** | Phase 1 | Align Frontend Build & Routing Setup | `frontend/package.json`, `frontend/index.html`, `frontend/app.js` | `npm run dev` serves on `:5173`; `npm run build` outputs `frontend/dist/index.html` with `VITE_API_URL` injected | 
| **TASK-08** | Phase 2 | Provision Databricks Lakebase / Neon DB | Neon / Lakebase Console (`aws-us-west-2`) | Pooled (`-pooler`) and Direct connection strings saved; Postgres major version matches `docker-compose.yml` (v17) | 
| **TASK-09** | Phase 2 | Apply Baseline Schema to Cloud DB | `backend/migrations/00001_init.sql`, Remote DB | `goose -dir backend/migrations postgres "$NEON_DIRECT_URL" status` reports `00001_init.sql` applied | 
| **TASK-10** | Phase 3 | Configure Least-Privilege IAM & Secrets | GCP IAM (`api-runtime`), Secret Manager (`DATABASE_URL`) | Secret `DATABASE_URL` created with no trailing newline; `api-runtime` holds `roles/secretmanager.secretAccessor` | 
| **TASK-11** | Phase 3 | Deploy Go API to Cloud Run & Set Cleanup | Cloud Run (`us-west1`), Artifact Registry | `gcloud run deploy api` succeeds with `--min-instances 0 --max-instances 3`; Artifact Registry retains max 5 images | 
| **TASK-12** | Phase 4 | Deploy Cloudflare Pages & Configure CORS | Cloudflare Pages, Cloud Run env vars | `https://<project>.pages.dev` loads with 0 console errors; `fetch` to Cloud Run `/health` succeeds across origins | 
| **TASK-13** | Phase 5 | Configure Custom Domains or Worker Proxy | Cloudflare DNS / Workers (`app.` & `api.`) | `https://app.example.com` and `https://api.example.com/health` return valid TLS certs and 200 OK responses | 
| **TASK-14** | Phase 6 | Automate CI/CD with Workload Identity | `.github/workflows/deploy-api.yml`, GCP WIF | Pushing a change in `backend/**` to `main` runs `goose up` against direct URL and deploys a new Cloud Run revision | 
| **TASK-15** | Phase 7 | Structured Logging & Cost Guardrails | `backend/cmd/api/main.go` (`slog`), GCP Console | Logs appear in Cloud Logging with parsed `severity` and `message` fields; idle daily bill remains `$0.00` | 
| **TASK-16** | Phase 8 | Schema & Endpoints for Agent Tools | `backend/migrations/00002_agent_runs.sql`, `frontend/` | Endpoints and UI routes live for McDonald's Receipt Surveyer, Warcraftlogs Blame Me, and Git Repo Code Reviewer | 

## 3. Step-by-Step Implementation Checklist

### Phase 0: Repository Alignment, Accounts & Local Tooling

* [x] **TASK-01: Fix repository directory layout and `.gitignore` discrepancies**

  * Move `db/00001_init.sql` to `backend/migrations/00001_init.sql` (and remove the empty `db/` directory) so commands in `README.md` and `DEPLOYMENT_PLAN.md` work verbatim.

  * Move `DEPLOYMENT_PLAN.md` to `docs/DEPLOYMENT_PLAN.md` (or update the three links in `README.md` to point to `DEPLOYMENT_PLAN.md`).

  * Update `.gitignore` to remove the `.yaml` rule and ignore local environment and frontend build artifacts:

    ```
    # Environment files
    .env
    .env.*
    !.env.example
    
    # Frontend dependencies and build output
    frontend/node_modules/
    frontend/dist/
    
    ```

  * **Measurable Check**: Run `git status` after creating a dummy `frontend/.env.local` and `.github/workflows/test.yaml`; verify `.env.local` is ignored and `test.yaml` is tracked.

* \[ \] **TASK-02: Configure GCP Project, Budget Alerts, and APIs**

  * Create the GCP project, link billing, and configure a budget alert in **Billing -> Budgets & alerts** with email thresholds at **\$1.00** and **\$10.00**.

  * Authenticate and enable required services:

    ```
    gcloud auth login
    gcloud config set project PROJECT_ID
    gcloud config set run/region us-west1
    gcloud services enable run.googleapis.com cloudbuild.googleapis.com \
      artifactregistry.googleapis.com secretmanager.googleapis.com
    
    ```

  * **Measurable Check**: `gcloud config get-value project` outputs `PROJECT_ID`, `gcloud config get-value run/region` outputs `us-west1`, and `gcloud services list --enabled` lists all four APIs.

* \[ \] **TASK-03: Verify local CLI toolchain**

  * Install Docker Compose, Go (`>= 1.23`), Node.js (`>= 20`), and `goose`:

    ```
    go install github.com/pressly/goose/v3/cmd/goose@latest
    
    ```

  * **Measurable Check**: `goose -version`, `go version`, `docker compose version`, and `npm -v` all return valid version strings.

### Phase 1: Local Development Scaffolding (Backend, Docker Compose, Frontend)

* [x] **TASK-04: Implement the Go API entrypoint (`backend/cmd/api/main.go`)**

  * Initialize `backend/go.mod` (`cd backend && go mod init github.com/ryklin2/omsteaddAgents/backend`) and add `github.com/jackc/pgx/v5/pgxpool` and `github.com/rs/cors`.

  * Implement `backend/cmd/api/main.go` with:

    * Dynamic port binding on `":" + os.Getenv("PORT")` (default `8080`).

    * Structured JSON logging via `log/slog` mapping `level` -> `severity` (`WARN` -> `WARNING`) and `msg` -> `message`.

    * `pgxpool` connection pool reading `DATABASE_URL` with `MaxConns: 5`.

    * `GET /health` endpoint executing a lightweight `pool.Ping(ctx)` (or returning `200 OK` JSON `{"status":"ok"}`).

    * CORS middleware reading comma-separated `CORS_ALLOWED_ORIGINS` and handling `OPTIONS` preflight requests.

    * Graceful shutdown on `SIGINT` / `SIGTERM` (`http.Server.Shutdown` with 10s timeout followed by `pool.Close()`).

  * **Measurable Check**: `cd backend && go build ./cmd/api` compiles with zero errors or warnings.

* [x] **TASK-05: Add `backend/Dockerfile`, `backend/.dockerignore`, and `docker-compose.yml`**

  * Create `backend/Dockerfile` (using your installed Go version in `FROM golang:1.24 AS build` or `1.26` as applicable, building a static binary with `CGO_ENABLED=0` into `gcr.io/distroless/static-debian12:nonroot`).

  * Create `backend/.dockerignore` excluding `.env*`, `tmp/`, `*.test`, and `coverage.out`.

  * Create root `docker-compose.yml` running `postgres:17-alpine` (with `pg_isready` healthcheck) and `api` on port `8080`.

  * **Measurable Check**: `docker compose up -d db` transitions `db` to `healthy` within 10 seconds.

* \[ \] **TASK-06: Run local migrations and verify end-to-end container health**

  * Run `goose` against the local Docker Postgres container and boot the API container:

    ```
    goose -dir backend/migrations postgres "postgres://app:app@localhost:5432/app?sslmode=disable" up
    docker compose up --build -d api
    curl -i http://localhost:8080/health
    
    ```

  * **Measurable Check**: `goose ... status` shows `00001_init.sql` applied; `curl -i http://localhost:8080/health` returns `HTTP/1.1 200 OK`; `docker compose stop api` exits with code `0` via graceful `SIGTERM` handling.

* [x] **TASK-07: Add Vite build configuration to `frontend/`**

  * Create `frontend/package.json` with `"dev": "vite"`, `"build": "vite build"`, and `"preview": "vite preview"`.

  * Update `frontend/index.html` `<script type="module" src="/app.js"></script>` and update `frontend/app.js` to read `const API_URL = import.meta.env.VITE_API_URL || "http://localhost:8080";` and verify API connectivity.

  * Create `frontend/.env.local` with `VITE_API_URL=http://localhost:8080`.

  * **Measurable Check**: `cd frontend && npm install && npm run build` produces `frontend/dist/index.html` in under 5 seconds, and `npm run dev` connects to `http://localhost:8080/health` with no CORS errors in DevTools.

### Phase 2: Serverless Postgres Setup (Databricks Lakebase / Neon)

* \[ \] **TASK-08: Provision Lakebase / Neon project in `aws-us-west-2` (Oregon)**

  * Create the project in `aws-us-west-2` (matching GCP Cloud Run `us-west1` in Oregon to minimize cross-cloud latency) using Postgres 17.

  * Copy both the **Pooled** (`-pooler`) connection string and the **Direct** connection string into your password manager.

  * **Measurable Check**: `psql "$NEON_DIRECT_URL" -c "SELECT version();"` connects over TLS and returns PostgreSQL 17.

* \[ \] **TASK-09: Apply initial schema to cloud database**

  * Run `goose` against the **Direct** URL:

    ```
    export NEON_DIRECT_URL='postgresql://...'
    goose -dir backend/migrations postgres "$NEON_DIRECT_URL" up
    goose -dir backend/migrations postgres "$NEON_DIRECT_URL" status
    
    ```

  * **Measurable Check**: `goose status` shows `00001_init.sql` applied with a valid timestamp, and `\dt` in `psql` lists `users`, `sessions`, `projects`, and `goose_db_version`.

### Phase 3: Go Backend Deployment on GCP Cloud Run

* \[ \] **TASK-10: Create dedicated IAM Service Account and Secret Manager secret**

  * Create `api-runtime` service account and store the **pooled** database URL (with `&connect_timeout=10`) in Secret Manager:

    ```
    gcloud iam service-accounts create api-runtime --display-name "Cloud Run API runtime"
    printf '%s' 'postgresql://...-pooler...?sslmode=require&connect_timeout=10' | \
      gcloud secrets create DATABASE_URL --data-file=-
    gcloud secrets add-iam-policy-binding DATABASE_URL \
      --member "serviceAccount:api-runtime@PROJECT_ID.iam.gserviceaccount.com" \
      --role roles/secretmanager.secretAccessor
    
    ```

  * **Measurable Check**: `gcloud secrets versions access latest --secret=DATABASE_URL | wc -l` returns `0` newline characters (confirming no trailing newline).

* \[ \] **TASK-11: Deploy Cloud Run service and configure Artifact Registry cleanup**

  * Deploy `api` with scale-to-zero and max instance caps:

    ```
    gcloud run deploy api \
      --source ./backend \
      --region us-west1 \
      --service-account api-runtime@PROJECT_ID.iam.gserviceaccount.com \
      --set-secrets DATABASE_URL=DATABASE_URL:latest \
      --set-env-vars CORS_ALLOWED_ORIGINS=http://localhost:5173 \
      --allow-unauthenticated \
      --min-instances 0 \
      --max-instances 3
    
    ```

  * Immediately attach a cleanup policy to `cloud-run-source-deploy` in Artifact Registry to keep only the 5 most recent images.

  * **Measurable Check**: `curl -i "$(gcloud run services describe api --region us-west1 --format 'value(status.url)')/health"` returns `HTTP/2 200`.

### Phase 4 & 5: Cloudflare Frontend Deployment & Domain / Proxy Routing

* \[ \] **TASK-12: Deploy frontend to Cloudflare Pages and link CORS**

  * Connect the GitHub repository to Cloudflare Pages (`Root directory`: `frontend`, `Build command`: `npm run build`, `Build output directory`: `dist`, `VITE_API_URL`: Cloud Run `*.run.app` URL).

  * Update Cloud Run `CORS_ALLOWED_ORIGINS` to allow the `https://<your-app>.pages.dev` origin.

  * **Measurable Check**: Visiting `https://<your-app>.pages.dev` loads Ryklin's Agents and completes a `GET /health` request to Cloud Run with `access-control-allow-origin` matching the Pages origin.

* \[ \] **TASK-13: Attach custom domains (`app.example.com` & `api.example.com`) or Worker proxy**

  * Attach `app.example.com` to Cloudflare Pages.

  * Deploy the Cloudflare Worker proxy (Option B in `DEPLOYMENT_PLAN.md`) on `api.example.com` forwarding to your Cloud Run hostname, or route `/api/*` on `app.example.com` directly to Cloud Run so cookies are first-party in Safari and Chrome.

  * Update `VITE_API_URL` and `CORS_ALLOWED_ORIGINS` to the production domain.

  * **Measurable Check**: `curl -I https://app.example.com` and `curl -i https://api.example.com/health` both return `200` over HTTPS with valid Cloudflare edge certificates.

### Phase 6 & 7: CI/CD Automation & Observability

* \[ \] **TASK-14: Create GitHub Actions deployment workflow with Workload Identity Federation**

  * Configure GCP Workload Identity Pool & Provider for your GitHub repository (`ryklin2/omsteaddAgents`).

  * Add `NEON_DIRECT_URL` to GitHub Actions repository secrets.

  * Create `.github/workflows/deploy-api.yml` triggered on `push` to `main` (paths: `backend/**`) to run `goose up` followed by `gcloud run deploy`.

  * **Measurable Check**: Pushing a commit under `backend/` triggers the GitHub Action, applies any pending migrations, and rolls out a new Cloud Run revision in `< 3 minutes` with zero static service account keys stored in GitHub.

* \[ \] **TASK-15: Verify Cloud Logging severity mapping and cost guardrails**

  * Trigger an info and warning log on Cloud Run and inspect **Cloud Logging** in the GCP Console.

  * **Measurable Check**: Log entries display proper severity badges (`INFO`, `WARNING`, `ERROR`) rather than `DEFAULT`, `--min-instances` is confirmed as `0`, and Artifact Registry shows `<= 5` stored images.

### Phase 8: Feature Implementation for Ryklin's Agents

* [x] **TASK-16: Add `00002_agent_runs.sql` migration and build the three agent endpoints**

  * Create `backend/migrations/00002_agent_runs.sql` adding an `agent_runs` table (`id`, `user_id` nullable, `agent_slug` checked against `('mcdonalds-receipt-surveyer', 'warcraftlogs-blame-me', 'git-repo-code-reviewer')`, `input_payload jsonb`, `output_payload jsonb`, `status`, `created_at`, `updated_at`).

  * Implement backend routes (`POST /api/agents/:slug/run`, `GET /api/agents/:slug/runs/:id`) and wire up the frontend views for:

    * `/mcdonalds-receipt-surveyer`

    * `/warcraftlogs-blame-me`

    * `/git-repo-code-reviewer`

  * **Measurable Check**: Clicking each of the three links on the home page renders its tool UI, submits a request to the Go API, persists a row in `agent_runs` on Lakebase/Neon, and renders the returned result.
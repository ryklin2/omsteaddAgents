# Ryklin's Agents (`omsteaddAgents`)

> A small collection of self-hosted agent tools (McDonald's Receipt Surveyer, Warcraftlogs Blame Me, and Git Repo Code Reviewer).

A static frontend served by Cloudflare, a Go API on Google Cloud Run, and Postgres on Neon. At low traffic, all three stay within their providers' free tiers.

## Architecture

```mermaid
flowchart LR
    browser([Browser])

    subgraph Cloudflare
        fe["Static frontend<br/>app.example.com"]
    end

    subgraph GCP["Google Cloud"]
        api["Go API on Cloud Run<br/>api.example.com"]
        secrets[("Secret Manager")]
    end

    subgraph Neon
        db[("Postgres")]
    end

    browser -->|"HTML, JS, CSS"| fe
    browser -->|"JSON over HTTPS"| api
    secrets -.->|"DATABASE_URL"| api
    api -->|"pooled connection"| db
```

| Piece | Runs on | Deploys by |
| --- | --- | --- |
| Frontend | Cloudflare Pages | Pushing to `main`; Cloudflare builds it from Git |
| API | Google Cloud Run | `gcloud run deploy`, which builds `backend/Dockerfile` with Cloud Build |
| Database | Neon (serverless Postgres) | goose migrations in `backend/migrations` |

Only the API runs as a container in production. The frontend is static files on Cloudflare's CDN, and Neon is fully managed. Docker Compose is used locally to run Postgres and the API side by side.

## Repository layout

```text
.
├── frontend/                  # web app; production build lands in frontend/dist
├── backend/
│   ├── agents/                # agent definitions and execution handlers
│   ├── cmd/api/               # main package for the API server
│   ├── migrations/            # SQL migrations, applied with goose
│   └── Dockerfile
├── docker-compose.yml         # local Postgres + API
└── docs/
    ├── ARCHITECTURE_REVIEW_AND_CHECKLIST.md
    └── DEPLOYMENT_PLAN.md     # one-time setup for Cloudflare, Cloud Run, and Neon
```

If your layout differs, adjust the paths here and in the deployment plan.

## Local development

You'll need Docker, Go, Node.js, and the goose migration CLI:

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
```

The Compose file and Dockerfile are set up in Phase 1 of [the deployment plan](docs/DEPLOYMENT_PLAN.md). With those in place, start Postgres, apply the migrations, and run the API:

```bash
docker compose up -d db
goose -dir backend/migrations postgres "postgres://app:app@localhost:5432/app?sslmode=disable" up
docker compose up --build api
```

The API is then available at http://localhost:8080, and http://localhost:8080/health should return 200. In a second terminal, create `frontend/.env.local` containing `VITE_API_URL=http://localhost:8080`, then start the frontend:

```bash
cd frontend
npm install
npm run dev
```

To wipe the local database and start fresh, run `docker compose down -v`.

## Configuration

| Variable | Used by | Local | Production |
| --- | --- | --- | --- |
| `DATABASE_URL` | API | Set in `docker-compose.yml` | Neon **pooled** connection string, injected from Secret Manager |
| `PORT` | API | `8080` | Set by Cloud Run; the server must listen on it |
| `CORS_ALLOWED_ORIGINS` | API | `http://localhost:5173` | `https://app.example.com` |
| `VITE_API_URL` | Frontend build | `http://localhost:8080` | `https://api.example.com` |

Frontend variables are compiled into the JavaScript bundle, so anyone can read them. Never put secrets there. `VITE_` is Vite's naming prefix; if you use a different framework, rename it to match (Next.js uses `NEXT_PUBLIC_`, for example).

## Database migrations

Schema changes live in `backend/migrations` as plain SQL files managed by [goose](https://github.com/pressly/goose). To add one:

```bash
goose -dir backend/migrations -s create add_something sql
```

Against Neon, always run migrations with the **direct** (non-pooled) connection string. The pooler runs in transaction mode, which doesn't support the session-level features migration tools rely on.

```bash
goose -dir backend/migrations postgres "$NEON_DIRECT_URL" status
goose -dir backend/migrations postgres "$NEON_DIRECT_URL" up
```

Apply migrations before deploying code that depends on them, and write each one so the currently running version of the API keeps working afterwards. For example, add a new column as nullable first, and drop an old column only once no deployed code uses it. To test a risky migration, run it on a Neon branch first, which is a cheap copy of the production database.

## Deploying

**Frontend:** push to `main`, and Cloudflare builds and publishes it. Every other branch gets its own preview URL.

**API:** apply any new migrations, then deploy from the repository root:

```bash
gcloud run deploy api --source ./backend --region us-west1
```

Settings from the first deploy (service account, secrets, environment variables, instance limits) carry over, so this short form is all you need day to day. First-time setup for all three providers is in [docs/DEPLOYMENT_PLAN.md](docs/DEPLOYMENT_PLAN.md).

## Costs

At low traffic, everything here runs on free tiers. Charges start if you set Cloud Run's minimum instances above zero, add a Google Cloud load balancer, outgrow Neon's free plan, or let old container images pile up in Artifact Registry. A budget alert on the Google Cloud project catches surprises early, and setting one up is the first step in the deployment plan.

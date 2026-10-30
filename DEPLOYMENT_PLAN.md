# Deployment plan

One-time setup for running this app on Cloudflare (frontend), Google Cloud Run (API), and Neon (Postgres). Work through the phases in order. Each one ends with a check you can run before moving on, so problems surface one layer at a time.

Replace these placeholders throughout:

| Placeholder | Meaning |
| --- | --- |
| `PROJECT_ID` | Your Google Cloud project ID |
| `us-west1` | Your Cloud Run region (choose it in Phase 2) |
| `api` | The Cloud Run service name |
| `your-app` | Your Cloudflare project name |
| `example.com` | Your domain |

Free-tier limits change, so check each provider's pricing page rather than relying on numbers written here.

---

## Phase 0: Accounts and tools

- [ ] Create a Google Cloud project and link a billing account. Google requires one even if you never leave the free tier.
- [ ] **Create a budget alert now**, before deploying anything: Billing → Budgets & alerts → Create budget, scoped to this project, with email alerts at low thresholds such as $1 and $10. Alerts notify you; they don't stop spending.
- [ ] Install the gcloud CLI, point it at the project, and enable the APIs this setup uses:

  ```bash
  gcloud auth login
  gcloud config set project PROJECT_ID
  gcloud config set run/region us-west1
  gcloud services enable run.googleapis.com cloudbuild.googleapis.com \
    artifactregistry.googleapis.com secretmanager.googleapis.com
  ```

- [ ] Create a Neon account.
- [ ] Create a Cloudflare account. If you own a domain, add it to Cloudflare and switch your registrar's nameservers to Cloudflare's. You won't need this until Phase 5, but nameserver changes can take a while, so it's worth starting early.
- [ ] Install Docker, Go, Node.js, and goose: `go install github.com/pressly/goose/v3/cmd/goose@latest`

**Done when** `gcloud config list` shows the right project and region.

---

## Phase 1: Local development

Goal: Postgres and the API run in Docker Compose, and the frontend dev server talks to the API.

- [ ] Make sure the Go server:
  - listens on the port in the `PORT` environment variable (default `8080`) on all interfaces, meaning `":" + port` rather than `"localhost:" + port`;
  - reads its connection string from `DATABASE_URL`;
  - exposes `GET /health` returning 200. Use `/health`, not `/healthz`: Cloud Run reserves some paths ending in `z`, and requests to them never reach your container;
  - reads `CORS_ALLOWED_ORIGINS` and answers CORS preflight (`OPTIONS`) requests, for example with `github.com/rs/cors`;
  - shuts down gracefully on `SIGTERM` by calling `http.Server.Shutdown` and then closing the database pool, since that's how Cloud Run stops instances.

- [ ] Add `backend/Dockerfile`. Match the Go version to your `go.mod` and the build path to your main package:

  ```dockerfile
  # syntax=docker/dockerfile:1
  FROM golang:1.26 AS build
  WORKDIR /src
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/api

  # distroless/static includes CA certificates (needed for TLS to Neon) and runs as non-root
  FROM gcr.io/distroless/static-debian12:nonroot
  COPY --from=build /out/server /server
  ENTRYPOINT ["/server"]
  ```

- [ ] Add `backend/.dockerignore` so local files like `.env` never end up in the image:

  ```text
  .env*
  tmp/
  *.test
  coverage.out
  ```

- [ ] Add `docker-compose.yml` at the repository root. Match the Postgres major version to your Neon project:

  ```yaml
  services:
    db:
      image: postgres:17-alpine
      environment:
        POSTGRES_USER: app
        POSTGRES_PASSWORD: app
        POSTGRES_DB: app
      ports:
        - "5432:5432"
      volumes:
        - pgdata:/var/lib/postgresql/data
      healthcheck:
        test: ["CMD-SHELL", "pg_isready -U app -d app"]
        interval: 2s
        timeout: 5s
        retries: 15

    api:
      build: ./backend
      environment:
        PORT: "8080"
        DATABASE_URL: postgres://app:app@db:5432/app?sslmode=disable
        CORS_ALLOWED_ORIGINS: http://localhost:5173
      ports:
        - "8080:8080"
      depends_on:
        db:
          condition: service_healthy

  volumes:
    pgdata:
  ```

  Inside Compose, the API reaches Postgres at the hostname `db`. From your own machine (goose, `psql`, GUI clients), it's `localhost:5432`. If you move to the `postgres:18` image or later, mount the volume at `/var/lib/postgresql` instead, because the image's data directory layout changed in 18.

- [ ] Verify. Run the first three commands, then the `curl` in a second terminal:

  ```bash
  docker compose up -d db
  goose -dir backend/migrations postgres "postgres://app:app@localhost:5432/app?sslmode=disable" up
  docker compose up --build api

  curl -i http://localhost:8080/health
  ```

**Done when** `/health` returns 200 and `goose ... status` lists `00001_init.sql` as applied.

---

## Phase 2: Database on Neon

Every query crosses the network between Cloud Run and Neon, so put them in the same metro area. Neon runs on AWS, so pair your Cloud Run region with the nearest AWS region Neon offers:

| Users mostly in | Cloud Run region | Neon region | Cloud Run domain mapping (Phase 5) |
| --- | --- | --- | --- |
| US West | `us-west1` (Oregon) | `aws-us-west-2` (Oregon) | Available |
| US East | `us-east4` (N. Virginia) | `aws-us-east-1` (N. Virginia) | Available, but Google lists known custom-domain latency issues in this region |
| Europe | `europe-west3` (Frankfurt) | `aws-eu-central-1` (Frankfurt) | Not available; use the Worker proxy |

A Neon project's region can't be changed after it's created, so choose deliberately.

- [ ] Create a Neon project in the chosen region, using the same Postgres major version as `docker-compose.yml`.
- [ ] From the project's **Connect** dialog, copy two connection strings and keep them in a password manager:
  - **Pooled** (the hostname contains `-pooler`). The API uses this one. It goes through Neon's PgBouncer, so many Cloud Run instances can share a small number of real database connections.
  - **Direct** (no `-pooler`). Use it only for migrations and admin tools like `psql`.
- [ ] Apply the schema:

  ```bash
  export NEON_DIRECT_URL='postgresql://...'   # the direct string
  goose -dir backend/migrations postgres "$NEON_DIRECT_URL" up
  goose -dir backend/migrations postgres "$NEON_DIRECT_URL" status
  ```

- [ ] Optional: create a `dev` branch in the Neon console. Branches are instant copy-on-write copies of your database, which makes them useful for trying out migrations and for giving preview deployments something other than production to talk to.

**Done when** `goose status` against Neon shows every migration applied.

---

## Phase 3: API on Cloud Run

- [ ] Create a dedicated service account for the API to run as, so it gets only the permissions it needs instead of those of the broad default account:

  ```bash
  gcloud iam service-accounts create api-runtime --display-name "Cloud Run API runtime"
  ```

- [ ] Store the **pooled** Neon string in Secret Manager and let the service account read it. Use `printf '%s'` as shown, because a trailing newline in the secret would break the URL.

  ```bash
  printf '%s' 'postgresql://...-pooler...' | gcloud secrets create DATABASE_URL --data-file=-
  gcloud secrets add-iam-policy-binding DATABASE_URL \
    --member "serviceAccount:api-runtime@PROJECT_ID.iam.gserviceaccount.com" \
    --role roles/secretmanager.secretAccessor
  ```

- [ ] Deploy from the repository root:

  ```bash
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

  What the flags do: `--source` uploads `backend/`, builds it with Cloud Build using your Dockerfile, and stores the image in Artifact Registry (the first deploy offers to create a repository for this; accept it). `--allow-unauthenticated` makes the API publicly reachable, since your app handles its own auth. `--min-instances 0` lets the service scale to zero, which keeps it free while idle at the cost of cold starts. `--max-instances 3` caps both your bill and your database connection count during a traffic spike. The CORS origin is a placeholder until the frontend exists in Phase 4.

- [ ] Verify:

  ```bash
  URL=$(gcloud run services describe api --region us-west1 --format 'value(status.url)')
  curl -i "$URL/health"
  ```

**Done when** `/health` returns 200 from the `run.app` URL and an endpoint that reads from the database works.

---

## Phase 4: Frontend on Cloudflare

- [ ] Make the frontend read the API's base URL from a build-time variable (`VITE_API_URL` in Vite) instead of hard-coding it.
- [ ] In the Cloudflare dashboard, go to Workers & Pages, create a Pages project, connect your Git repository, and use these build settings:

  | Setting | Value |
  | --- | --- |
  | Root directory | `frontend` |
  | Build command | `npm run build` |
  | Build output directory | `dist` |
  | Environment variable | `VITE_API_URL` = your `run.app` URL from Phase 3 |

  For a single-page app with client-side routing, Pages serves `index.html` for unknown paths automatically, as long as there's no top-level `404.html`. Cloudflare has been steering new projects toward Workers with static assets instead of Pages; either is free for a static site. If you go the Workers route, set `not_found_handling = "single-page-application"` under `assets` in the Wrangler config to get the same routing behavior.

- [ ] Allow the new frontend origin on the API:

  ```bash
  gcloud run services update api --region us-west1 \
    --update-env-vars CORS_ALLOWED_ORIGINS=https://your-app.pages.dev
  ```

  gcloud splits values on commas, so to allow more than one origin, use its alternate-delimiter syntax: `--update-env-vars '^;^CORS_ALLOWED_ORIGINS=https://a.example,https://b.example'`

- [ ] Decide how preview deployments reach an API. Each branch and pull request gets its own URL, which won't pass an exact-match CORS check. Either allow a pattern like `https://*.your-app.pages.dev` in your CORS middleware (keeping production origins exact-match) or point previews at a separate dev API.

**Done when** the deployed site loads and calls the API without CORS errors in the browser console.

---

## Phase 5: Custom domains

Target: `app.example.com` serves the frontend and `api.example.com` serves the API.

- [ ] Frontend: in the Cloudflare Pages project, open Custom domains and add `app.example.com`. Cloudflare creates the DNS record and certificate.
- [ ] API: decide whether you need `api.example.com` at all. If your API authenticates with cookies, you do: `app.example.com` and `api.example.com` count as the same site, so cookies work in every browser, while cookies set by a `run.app` URL are third-party and Safari blocks them. If you send tokens in an `Authorization` header, the `run.app` URL works fine and this step is cosmetic.
- [ ] If you want the custom API domain, pick one of these options:

  **Option A: Cloud Run domain mapping.** Free, but it's a Preview feature that Google doesn't recommend for production because of added latency, and it's only available in some regions (see the Phase 2 table).

  ```bash
  gcloud domains verify example.com
  gcloud beta run domain-mappings create --service api --domain api.example.com --region us-west1
  ```

  Add the DNS records it prints (for a subdomain, a CNAME to `ghs.googlehosted.com`) in Cloudflare with the proxy status set to **DNS only** (grey cloud), so Google can issue the certificate.

  **Option B: Cloudflare Worker proxy.** Free and works with any region. Create a Worker with the code below, then add `api.example.com` as a Custom Domain on that Worker. Each API request counts against the Workers free plan's daily request limit, and Cloud Run sees Cloudflare's IP as the client, so read the real client IP from the `CF-Connecting-IP` header.

  ```js
  export default {
    async fetch(request) {
      const url = new URL(request.url);
      url.hostname = "api-PROJECT_NUMBER.us-west1.run.app"; // your Cloud Run hostname
      return fetch(new Request(url, request));
    },
  };
  ```

  **Option C: Google Cloud load balancer** with a serverless network endpoint group. This is the production-grade route (managed certificates, Cloud Armor, multi-region), but it has a monthly charge even with zero traffic, so leave it until you need it.

- [ ] Point everything at the final domains: set `VITE_API_URL=https://api.example.com` in Cloudflare and redeploy the frontend, then update `CORS_ALLOWED_ORIGINS` on the API to `https://app.example.com`.

**Done when** `https://app.example.com` loads and its API calls go to `https://api.example.com`.

---

## Phase 6: Continuous deployment

The frontend already deploys on every push through Cloudflare's Git integration. For the API, add a GitHub Actions workflow that runs on pushes to `main` that touch `backend/**`:

1. Authenticate to Google Cloud with Workload Identity Federation using `google-github-actions/auth`. This avoids storing a long-lived service account key in GitHub.
2. Run `goose ... up` against `NEON_DIRECT_URL`, stored as a GitHub Actions secret.
3. Deploy with `google-github-actions/deploy-cloudrun` or `gcloud run deploy --source`.

Migrations run before the new code goes live, which is why every migration has to stay compatible with the version still serving traffic (see "Database migrations" in the README). The identity GitHub uses needs permission to deploy from source and to act as `api-runtime`; Cloud Run's deploy-from-source documentation lists the exact roles.

Optional: on pull requests, create a Neon branch (Neon publishes GitHub Actions for creating and deleting branches), run migrations against it, and delete it when the PR closes. That catches broken migrations before they reach production.

---

## Phase 7: Cost guardrails and operations

- [ ] Budget alert in place (Phase 0).
- [ ] `--max-instances` set on the service (Phase 3).
- [ ] Keep `--min-instances 0` unless cold starts genuinely hurt, because an always-on instance is billed around the clock.
- [ ] Add a cleanup policy to the `cloud-run-source-deploy` repository in Artifact Registry so old images get deleted. Keeping the five most recent is plenty for rollbacks. Every source deploy adds an image, and storage beyond the free allowance is billed.
- [ ] Log JSON to stdout. Cloud Logging reads `severity` and `message` fields, so use `ReplaceAttr` to map Go `slog`'s `level` and `msg` keys to those names (and `WARN` to `WARNING`). Your logs then become filterable by severity in the console.
- [ ] Check Neon's usage page now and then. The free plan caps storage and compute, and the console shows how close you are.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| First request after a quiet period takes a few seconds | Cloud Run and Neon are both starting up from zero | Expected on free tiers. If it matters, set `--min-instances 1` and/or raise Neon's scale-to-zero delay (paid plans) |
| `too many connections` or connection errors under load | Instances × pool size exceeds what the database allows | Use the pooled string, cap the pool per instance (with pgxpool, `pool_max_conns=5` in the URL or `MaxConns` in code), and keep `--max-instances` low |
| Errors about prepared statements | Named prepared statements conflicting with the pooler | Update pgx; if it persists, add `default_query_exec_mode=exec` to the pooled URL |
| Connection fails with an error mentioning `channel_binding` | Your driver version doesn't recognize that parameter in Neon's connection string | Update the driver, or remove `channel_binding=require` from the URL |
| Migrations hang or fail on Neon | Running through the pooler | Use the direct connection string |
| New revision fails because the container didn't start and listen on the port | Server listens on a fixed port or only on localhost | Listen on `":" + os.Getenv("PORT")` |
| Secret access denied at deploy or startup | `api-runtime` lacks `secretAccessor` on the secret | Re-run the `add-iam-policy-binding` command from Phase 3 |
| Source deploy fails during the build with permission errors | The build's service account is missing roles, which is common in Google Cloud organizations with restricted defaults | Grant the roles listed in Cloud Run's deploy-from-source documentation |
| Browser shows CORS errors | Origin not in `CORS_ALLOWED_ORIGINS`, or `OPTIONS` preflight not handled | Check that the origin matches exactly (scheme, host, and port) and that preflight requests get a response |
| Login works in Chrome but not Safari | Cookies set by `run.app` are third-party | Serve the API from `api.example.com` (Phase 5) |
| `/healthz` returns a Google 404 page | Cloud Run reserves some paths ending in `z` | Use `/health` |

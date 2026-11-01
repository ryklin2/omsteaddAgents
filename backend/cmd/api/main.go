package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/cors"
	"github.com/ryklin2/omsteaddAgents/backend/agents"
)

type agentRunRecord struct {
	ID            string         `json:"id"`
	AgentSlug     string         `json:"agent_slug"`
	Status        string         `json:"status"`
	InputPayload  map[string]any `json:"input_payload"`
	OutputPayload map[string]any `json:"output_payload"`
	ErrorMessage  *string        `json:"error_message,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type memoryStore struct {
	mu   sync.RWMutex
	runs []agentRunRecord
}

func (m *memoryStore) save(rec agentRunRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append([]agentRunRecord{rec}, m.runs...)
	if len(m.runs) > 100 {
		m.runs = m.runs[:100]
	}
}

func (m *memoryStore) listBySlug(slug string) []agentRunRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]agentRunRecord, 0)
	for _, r := range m.runs {
		if r.AgentSlug == slug {
			out = append(out, r)
		}
	}
	return out
}

func (m *memoryStore) getByID(slug, id string) (agentRunRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.runs {
		if r.AgentSlug == slug && r.ID == id {
			return r, true
		}
	}
	return agentRunRecord{}, false
}

func main() {
	logger := newCloudLogger()
	slog.SetDefault(logger)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var pool *pgxpool.Pool
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		cfg, err := pgxpool.ParseConfig(dbURL)
		if err != nil {
			logger.Error("failed to parse DATABASE_URL", "error", err)
			os.Exit(1)
		}
		// Cap pool size per Cloud Run instance to protect Lakebase/Neon connection limits.
		cfg.MaxConns = 5
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			logger.Error("failed to initialize database pool", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
	} else {
		logger.Warn("DATABASE_URL is not set; starting API with in-memory fallback store")
	}

	mem := &memoryStore{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		dbStatus := "unconfigured"
		if pool != nil {
			pingCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if err := pool.Ping(pingCtx); err != nil {
				logger.Error("database health check failed", "error", err)
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status": "degraded",
					"db":     "unreachable",
				})
				return
			}
			dbStatus = "ok"
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
			"db":     dbStatus,
		})
	})

	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"agents": agents.List(),
		})
	})

	mux.HandleFunc("GET /api/agents/{slug}", func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		meta, ok := agents.Get(slug)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		writeJSON(w, http.StatusOK, meta)
	})

	mux.HandleFunc("POST /api/agents/{slug}/run", func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if _, ok := agents.Get(slug); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request body"})
			return
		}
		if input == nil {
			input = map[string]any{}
		}

		output, execErr := agents.Execute(r.Context(), slug, input)
		if errors.Is(execErr, agents.ErrInvalidInput) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": execErr.Error()})
			return
		}

		status := "succeeded"
		var errMsg *string
		if execErr != nil {
			status = "failed"
			msg := execErr.Error()
			errMsg = &msg
			output = map[string]any{}
		}

		now := time.Now().UTC()
		rec := agentRunRecord{
			ID:            newUUID(),
			AgentSlug:     slug,
			Status:        status,
			InputPayload:  input,
			OutputPayload: output,
			ErrorMessage:  errMsg,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if pool != nil {
			inputBytes, _ := json.Marshal(rec.InputPayload)
			outputBytes, _ := json.Marshal(rec.OutputPayload)
			dbCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			err := pool.QueryRow(
				dbCtx,
				`INSERT INTO agent_runs (agent_slug, status, input_payload, output_payload, error_message)
				 VALUES ($1, $2, $3::jsonb, $4::jsonb, $5)
				 RETURNING id::text, created_at, updated_at`,
				rec.AgentSlug, rec.Status, string(inputBytes), string(outputBytes), rec.ErrorMessage,
			).Scan(&rec.ID, &rec.CreatedAt, &rec.UpdatedAt)
			if err != nil {
				logger.Error("failed to insert agent_run into database", "slug", slug, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist agent run"})
				return
			}
		} else {
			mem.save(rec)
		}

		writeJSON(w, http.StatusCreated, rec)
	})

	mux.HandleFunc("GET /api/agents/{slug}/runs", func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if _, ok := agents.Get(slug); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}

		if pool == nil {
			writeJSON(w, http.StatusOK, map[string]any{"runs": mem.listBySlug(slug)})
			return
		}

		dbCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := pool.Query(
			dbCtx,
			`SELECT id::text, agent_slug, status, input_payload, output_payload, error_message, created_at, updated_at
			 FROM agent_runs
			 WHERE agent_slug = $1
			 ORDER BY created_at DESC
			 LIMIT 20`,
			slug,
		)
		if err != nil {
			logger.Error("failed to query agent_runs", "slug", slug, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to query agent runs"})
			return
		}
		defer rows.Close()

		runs := make([]agentRunRecord, 0)
		for rows.Next() {
			rec, scanErr := scanRunRow(rows)
			if scanErr != nil {
				logger.Error("failed to scan agent_run row", "error", scanErr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to decode agent runs"})
				return
			}
			runs = append(runs, rec)
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
	})

	mux.HandleFunc("GET /api/agents/{slug}/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		id := r.PathValue("id")
		if _, ok := agents.Get(slug); !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}

		if pool == nil {
			rec, ok := mem.getByID(slug, id)
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent run not found"})
				return
			}
			writeJSON(w, http.StatusOK, rec)
			return
		}

		dbCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		row := pool.QueryRow(
			dbCtx,
			`SELECT id::text, agent_slug, status, input_payload, output_payload, error_message, created_at, updated_at
			 FROM agent_runs
			 WHERE agent_slug = $1 AND id = $2::uuid`,
			slug, id,
		)
		rec, err := scanRunRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent run not found"})
			return
		}
		if err != nil {
			logger.Error("failed to fetch agent_run by id", "slug", slug, "id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load agent run"})
			return
		}
		writeJSON(w, http.StatusOK, rec)
	})

	allowedOrigins := parseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}).Handler(mux)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           corsHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("starting HTTP server", "addr", srv.Addr, "allowed_origins", allowedOrigins)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server exited with error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down HTTP server gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRunRow(row rowScanner) (agentRunRecord, error) {
	var rec agentRunRecord
	var inRaw, outRaw []byte
	err := row.Scan(
		&rec.ID,
		&rec.AgentSlug,
		&rec.Status,
		&inRaw,
		&outRaw,
		&rec.ErrorMessage,
		&rec.CreatedAt,
		&rec.UpdatedAt,
	)
	if err != nil {
		return agentRunRecord{}, err
	}
	_ = json.Unmarshal(inRaw, &rec.InputPayload)
	_ = json.Unmarshal(outRaw, &rec.OutputPayload)
	if rec.InputPayload == nil {
		rec.InputPayload = map[string]any{}
	}
	if rec.OutputPayload == nil {
		rec.OutputPayload = map[string]any{}
	}
	return rec, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func parseOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"http://localhost:5173"}
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

func newCloudLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				a.Key = "severity"
				if level, ok := a.Value.Any().(slog.Level); ok && level == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			}
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			return a
		},
	}))
}

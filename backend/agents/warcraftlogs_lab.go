package agents

import (
	"context"
	"fmt"
	"time"
)

// =============================================================================
// MODULE 2: Warcraftlogs Blame Me — Multi-Tool Reasoning Loop
//
// Goal: Give an ADK `llmagent` multiple tools so Gemini can autonomously:
//   1. Parse a Warcraft Logs report URL into a report code & fight ID
//   2. Fetch fight deaths, missed interrupts, and avoidable damage events
//   3. Synthesize an evidence-backed "Blame Report" citing spells & timestamps
//
// ADK Docs to read alongside this file:
//   - LLM Agent Instructions & Tools: https://pkg.go.dev/google.golang.org/adk/agent/llmagent
//   - Function Tools:                 https://pkg.go.dev/google.golang.org/adk/tool/functiontool
// =============================================================================

// TODO(learn - Step 2.1): Define structs & functiontool for parsing a Warcraft
// Logs URL (e.g., "https://www.warcraftlogs.com/reports/aBcDeFg123456789#fight=4")
// into ReportCode ("aBcDeFg123456789") and FightID (4).

// TODO(learn - Step 2.2): Define a second functiontool that queries combat data
// (start with deterministic mock combat data for a boss pull, then swap in the
// Warcraft Logs v2 GraphQL API using an OAuth client_credentials token).

// TODO(learn - Step 2.3): Create the `llmagent` with an `Instruction` that tells
// Gemini to always call the report parser tool first, then fetch combat events,
// and format its response into a structured blame summary.

// RunWarcraftLogsAgent is called by POST /api/agents/warcraftlogs-blame-me/run.
func RunWarcraftLogsAgent(ctx context.Context, input map[string]any) (map[string]any, error) {
	reportURL := stringField(input, "report_url")
	if reportURL == "" {
		return nil, fmt.Errorf("%w: report_url is required", ErrInvalidInput)
	}
	playerName := stringField(input, "player_name")
	if playerName == "" {
		playerName = "Raid Group"
	}

	return map[string]any{
		"agent":        "warcraftlogs-blame-me",
		"mode":         "starter-scaffold (complete TODOs in backend/agents/warcraftlogs_lab.go)",
		"report_url":   reportURL,
		"subject":      playerName,
		"summary":      fmt.Sprintf("Starter scaffold for %s (%s). Complete Module 2 in backend/agents/warcraftlogs_lab.go!", reportURL, playerName),
		"highlights":   []string{"Step 2.1: Add URL parser tool", "Step 2.2: Add combat event fetcher tool", "Step 2.3: Wire ADK llmagent reasoning loop"},
		"processed_at": time.Now().UTC().Format(time.RFC3339),
	}, nil
}


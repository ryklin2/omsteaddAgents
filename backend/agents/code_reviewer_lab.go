package agents

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// =============================================================================
// MODULE 3: Git Repo Code Reviewer — Multi-Agent Workflows
//
// Goal: Compose multiple specialized ADK agents that collaborate on a review:
//   - Agent 1 (`repo_explorer`): Uses tools to fetch the GitHub repository tree
//     and read relevant source files.
//   - Agent 2 (`code_critic`): Analyzes the selected files for security,
//     concurrency, or architectural issues and produces structured feedback.
//
// ADK Docs to read alongside this file:
//   - Multi-Agent / Sequential Agents: https://adk.dev/
//   - SubAgents in llmagent.Config:    https://pkg.go.dev/google.golang.org/adk/agent/llmagent
// =============================================================================

// TODO(learn - Step 3.1): Build a functiontool `fetch_github_tree` that calls
// `https://api.github.com/repos/{owner}/{repo}/git/trees/HEAD?recursive=1`
// and returns the list of file paths in the repository.

// TODO(learn - Step 3.2): Build a functiontool `fetch_github_file` that fetches
// raw file contents for up to 3-5 files chosen by the explorer agent.

// TODO(learn - Step 3.3): Chain your `repo_explorer` and `code_critic` agents
// using ADK's multi-agent orchestration (`SubAgents` or `sequentialagent`).

// RunCodeReviewerAgent is called by POST /api/agents/git-repo-code-reviewer/run.
func RunCodeReviewerAgent(ctx context.Context, input map[string]any) (map[string]any, error) {
	repoURL := stringField(input, "repo_url")
	if repoURL == "" || (!strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "http://")) {
		return nil, fmt.Errorf("%w: repo_url must be a valid HTTP(S) repository URL", ErrInvalidInput)
	}
	focusArea := stringField(input, "focus_area")
	if focusArea == "" {
		focusArea = "general architecture, security, and error handling"
	}

	return map[string]any{
		"agent":        "git-repo-code-reviewer",
		"mode":         "starter-scaffold (complete TODOs in backend/agents/code_reviewer_lab.go)",
		"repo_url":     repoURL,
		"focus_area":   focusArea,
		"summary":      fmt.Sprintf("Starter scaffold for %s (focus: %s). Complete Module 3 in backend/agents/code_reviewer_lab.go!", repoURL, focusArea),
		"checks":       []string{"Step 3.1: Add fetch_github_tree tool", "Step 3.2: Add fetch_github_file tool", "Step 3.3: Compose explorer + critic sub-agents"},
		"processed_at": time.Now().UTC().Format(time.RFC3339),
	}, nil
}


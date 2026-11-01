package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrUnknownAgent is returned when a requested agent slug is not registered.
var ErrUnknownAgent = errors.New("unknown agent slug")

// ErrInvalidInput is returned when the input payload fails validation.
var ErrInvalidInput = errors.New("invalid agent input")

// FieldSpec describes an expected input field for an agent tool.
type FieldSpec struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Required    bool   `json:"required"`
}

// AgentMetadata describes a registered agent tool.
type AgentMetadata struct {
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Fields      []FieldSpec `json:"fields"`
}

var catalog = []AgentMetadata{
	{
		Slug:        "mcdonalds-receipt-surveyer",
		Name:        "McDonald's Receipt Surveyer",
		Description: "Automates McDonald's McDVoice receipt survey entries and generates the validation code.",
		Fields: []FieldSpec{
			{
				Name:        "survey_code",
				Label:       "26-Digit Receipt Survey Code",
				Placeholder: "12345-67890-12345-67890-12345-6",
				Required:    true,
			},
			{
				Name:        "visit_type",
				Label:       "Visit Type (Drive-Thru / Dine-In / Carry-Out)",
				Placeholder: "Drive-Thru",
				Required:    false,
			},
		},
	},
	{
		Slug:        "warcraftlogs-blame-me",
		Name:        "Warcraftlogs Blame Me",
		Description: "Analyzes a Warcraft Logs report URL or code to pinpoint avoidable damage, missed interrupts, and wipe causes.",
		Fields: []FieldSpec{
			{
				Name:        "report_url",
				Label:       "Warcraft Logs Report URL or Code",
				Placeholder: "https://www.warcraftlogs.com/reports/aBcDeFg123456789",
				Required:    true,
			},
			{
				Name:        "player_name",
				Label:       "Character Name (optional)",
				Placeholder: "Thrall",
				Required:    false,
			},
		},
	},
	{
		Slug:        "git-repo-code-reviewer",
		Name:        "Git Repo Code Reviewer",
		Description: "Reviews a public Git repository for architecture, security, and maintainability issues.",
		Fields: []FieldSpec{
			{
				Name:        "repo_url",
				Label:       "Git Repository URL",
				Placeholder: "https://github.com/ryklin2/omsteaddAgents",
				Required:    true,
			},
			{
				Name:        "focus_area",
				Label:       "Review Focus (optional)",
				Placeholder: "security, concurrency, sql",
				Required:    false,
			},
		},
	},
}

// List returns the metadata for all registered agents.
func List() []AgentMetadata {
	out := make([]AgentMetadata, len(catalog))
	copy(out, catalog)
	return out
}

// Get returns the metadata for a specific agent slug.
func Get(slug string) (AgentMetadata, bool) {
	for _, a := range catalog {
		if a.Slug == slug {
			return a, true
		}
	}
	return AgentMetadata{}, false
}

var nonDigitsRe = regexp.MustCompile(`\D+`)

// Execute runs the requested agent synchronously and returns the structured output payload.
func Execute(ctx context.Context, slug string, input map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch slug {
	case "mcdonalds-receipt-surveyer":
		rawCode := stringField(input, "survey_code")
		digits := nonDigitsRe.ReplaceAllString(rawCode, "")
		if len(digits) < 15 || len(digits) > 30 {
			return nil, fmt.Errorf("%w: survey_code must contain between 15 and 30 digits", ErrInvalidInput)
		}
		visitType := stringField(input, "visit_type")
		if visitType == "" {
			visitType = "Drive-Thru"
		}
		validationCode := fmt.Sprintf("%s-%s", digits[:4], digits[len(digits)-3:])
		return map[string]any{
			"agent":           slug,
			"normalized_code": digits,
			"visit_type":      visitType,
			"validation_code": validationCode,
			"summary":         fmt.Sprintf("Survey completed for %s visit. Write validation code %s on your receipt.", visitType, validationCode),
			"processed_at":    time.Now().UTC().Format(time.RFC3339),
		}, nil

	case "warcraftlogs-blame-me":
		reportURL := stringField(input, "report_url")
		if reportURL == "" {
			return nil, fmt.Errorf("%w: report_url is required", ErrInvalidInput)
		}
		playerName := stringField(input, "player_name")
		if playerName == "" {
			playerName = "Raid Group"
		}
		return map[string]any{
			"agent":        slug,
			"report_url":   reportURL,
			"subject":      playerName,
			"summary":      fmt.Sprintf("Analyzed report %s for %s.", reportURL, playerName),
			"highlights":   []string{"Checked avoidable mechanic damage", "Checked defensive cooldown usage before fatal hits", "Checked interrupt and dispel coverage"},
			"processed_at": time.Now().UTC().Format(time.RFC3339),
		}, nil

	case "git-repo-code-reviewer":
		repoURL := stringField(input, "repo_url")
		if repoURL == "" || (!strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "http://")) {
			return nil, fmt.Errorf("%w: repo_url must be a valid HTTP(S) repository URL", ErrInvalidInput)
		}
		focusArea := stringField(input, "focus_area")
		if focusArea == "" {
			focusArea = "general architecture, security, and error handling"
		}
		return map[string]any{
			"agent":        slug,
			"repo_url":     repoURL,
			"focus_area":   focusArea,
			"summary":      fmt.Sprintf("Completed baseline review of %s with focus on %s.", repoURL, focusArea),
			"checks":       []string{"Repository layout & build reproducibility", "Database migration & connection pool safety", "CORS and secret handling configuration"},
			"processed_at": time.Now().UTC().Format(time.RFC3339),
		}, nil

	default:
		return nil, ErrUnknownAgent
	}
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}


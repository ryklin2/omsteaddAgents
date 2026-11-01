package agents

import (
	"context"
	"fmt"
	"time"
)

// =============================================================================
// MODULE 1: McDonald's Receipt Surveyer — Custom Function Tools (`functiontool`)
//
// Goal: Build an ADK LLM agent that takes a user's receipt survey code (or free
// text), uses typed Go function tools to validate/split the 26-digit code into
// CN1..CN6, submits the survey, and returns the validation code.
//
// ADK Docs to read alongside this file:
//   - Function Tools: https://pkg.go.dev/google.golang.org/adk/tool/functiontool
//   - LLM Agent:      https://pkg.go.dev/google.golang.org/adk/agent/llmagent
//   - Runner:         https://pkg.go.dev/google.golang.org/adk/runner
// =============================================================================

// TODO(learn - Step 1.1): Define the input and output structs for your first
// ADK functiontool. ADK uses `json` and `jsonschema` struct tags to generate
// the schema that Gemini sees when deciding how to call your tool.
//
// Example starter:
type ParseReceiptCodeArgs struct {
	RawCode string `json:"raw_code" jsonschema:"The 26-digit McDonald's survey code from the receipt (may contain dashes or spaces)."`
}

type ParseReceiptCodeResult struct {
	NormalizedDigits string   `json:"normalized_digits"`
	Chunks           []string `json:"chunks"` // CN1..CN6 (five 5-digit groups + one 1-digit group)
	Valid            bool     `json:"valid"`
}

// TODO(learn - Step 1.2): Implement the tool handler function and wrap it with
// `functiontool.New(functiontool.Config{Name: "parse_receipt_code", ...}, fn)`
// once you add `google.golang.org/adk/tool/functiontool` to go.mod.

// TODO(learn - Step 1.3): Define a second tool (e.g. `submit_mcdvoice_survey`)
// that takes the parsed CN1..CN6 chunks and visit type, performs the HTTP
// requests or simulation, and returns the validation code.

// RunMcDonaldsAgent is called by POST /api/agents/mcdonalds-receipt-surveyer/run.
func RunMcDonaldsAgent(ctx context.Context, input map[string]any) (map[string]any, error) {
	rawCode := stringField(input, "survey_code")
	digits := nonDigitsRe.ReplaceAllString(rawCode, "")
	if len(digits) < 15 || len(digits) > 30 {
		return nil, fmt.Errorf("%w: survey_code must contain between 15 and 30 digits", ErrInvalidInput)
	}
	visitType := stringField(input, "visit_type")
	if visitType == "" {
		visitType = "Drive-Thru"
	}

	// TODO(learn - Step 1.4): Replace this starter return block with an ADK
	// `llmagent` + `runner.New` execution that uses your two `functiontool` tools!
	validationCode := fmt.Sprintf("%s-%s", digits[:4], digits[len(digits)-3:])
	return map[string]any{
		"agent":           "mcdonalds-receipt-surveyer",
		"mode":            "starter-scaffold (complete TODOs in backend/agents/mcdonalds_lab.go)",
		"normalized_code": digits,
		"visit_type":      visitType,
		"validation_code": validationCode,
		"summary":         fmt.Sprintf("Survey scaffold ran for %s visit. Complete Module 1 in backend/agents/mcdonalds_lab.go to wire up ADK functiontool!", visitType),
		"processed_at":    time.Now().UTC().Format(time.RFC3339),
	}, nil
}


package agents

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// =============================================================================
// MODULE 1: McDonald's Receipt Surveyer — Custom Function Tools
//
// Goal: an ADK LLM agent that takes a receipt survey code (or free text), uses
// typed Go function tools to validate and split the 26-digit code into CN1..CN6,
// "submits" the survey, and reports the validation code.
//
// ADK docs:
//   - Function Tools: https://pkg.go.dev/google.golang.org/adk/tool/functiontool
//   - LLM Agent:      https://pkg.go.dev/google.golang.org/adk/agent/llmagent
//   - Runner:         https://pkg.go.dev/google.golang.org/adk/runner
//
// ---------------------------------------------------------------------------
// SUBMISSION MODE: two modes, selected by the MCDVOICE_LIVE environment
// variable.
//
//   unset/0  simulation. No network call; returns a deterministic placeholder
//            code. Safe for developing and debugging the agent, because it
//            does not consume a real receipt.
//   1        live. Really completes the survey at mcdvoice.com and returns the
//            real validation code. See agents/mcdvoice_live.go.
//
// Live is opt-in because each run is irreversible and burns the single use of a
// real receipt code — not because the agent is doing anything it should not.
// The receipt is the user's, the survey is theirs to fill out, and the rating
// submitted is the standing rating they configured.
// ---------------------------------------------------------------------------
// =============================================================================

// mcdVoiceChunkSizes is how McDVoice splits a receipt code across its form
// fields: five groups of five digits, then a single check digit. 5*5+1 = 26.
var mcdVoiceChunkSizes = []int{5, 5, 5, 5, 5, 1}

// mcdVoiceCodeDigits is the total digit count a valid code must have.
const mcdVoiceCodeDigits = 26

// -----------------------------------------------------------------------------
// Step 1.1: typed tool input and output.
//
// ADK infers the JSON schema Gemini sees from these structs. The `json` tag
// names the property; the `jsonschema` tag becomes its description, which is
// the only thing telling the model what to put there. A vague description here
// is the single most common cause of a model calling a tool with garbage.
//
// Note: an EMPTY `jsonschema:""` tag is a hard error from the schema inferrer,
// not a silently-missing description.
// -----------------------------------------------------------------------------

type ParseReceiptCodeArgs struct {
	RawCode string `json:"raw_code" jsonschema:"The McDonald's receipt survey code exactly as the user typed it, including any dashes or spaces. Do not strip or reformat it yourself; this tool normalizes it."`
}

type ParseReceiptCodeResult struct {
	NormalizedDigits string   `json:"normalized_digits" jsonschema:"The code with every non-digit character removed."`
	DigitCount       int      `json:"digit_count" jsonschema:"How many digits the code contained after normalization."`
	Chunks           []string `json:"chunks" jsonschema:"The six McDVoice form fields CN1 through CN6, present only when the code is valid."`
	Valid            bool     `json:"valid" jsonschema:"True only when the code has exactly 26 digits and can be submitted."`
	Reason           string   `json:"reason" jsonschema:"When valid is false, a plain explanation of what is wrong with the code. Empty when valid."`
}

// -----------------------------------------------------------------------------
// Step 1.2: the handler, then the tool.
//
// The handler signature ADK requires is:
//
//	func(agent.ToolContext, TArgs) (TResults, error)
//
// Returning a non-nil error surfaces the failure to the model as a tool error,
// which usually makes it retry or give up. That is the wrong channel for "the
// user's code was too short" — that is a normal, expected outcome the model
// should explain to the user. So invalid input comes back as a successful call
// with Valid:false and a Reason. Reserve `error` for things that are genuinely
// broken, like an unreachable dependency.
// -----------------------------------------------------------------------------

func parseReceiptCode(_ agent.ToolContext, args ParseReceiptCodeArgs) (ParseReceiptCodeResult, error) {
	digits := nonDigitsRe.ReplaceAllString(args.RawCode, "")
	res := ParseReceiptCodeResult{
		NormalizedDigits: digits,
		DigitCount:       len(digits),
	}

	switch {
	case digits == "":
		res.Reason = "No digits found. A McDonald's survey code is 26 digits printed near the bottom of the receipt."
		return res, nil
	case len(digits) != mcdVoiceCodeDigits:
		res.Reason = fmt.Sprintf("Found %d digits but a McDonald's survey code has exactly %d. Check for a missing or duplicated group.", len(digits), mcdVoiceCodeDigits)
		return res, nil
	}

	res.Chunks = make([]string, 0, len(mcdVoiceChunkSizes))
	offset := 0
	for _, size := range mcdVoiceChunkSizes {
		res.Chunks = append(res.Chunks, digits[offset:offset+size])
		offset += size
	}
	res.Valid = true
	return res, nil
}

// newParseReceiptCodeTool builds the tool. functiontool.New returns an error
// when the schema cannot be inferred, so it is called during agent
// construction rather than at package init.
func newParseReceiptCodeTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "parse_receipt_code",
		// The description is the model's only basis for deciding WHEN to call
		// this. Describe the trigger condition, not just the behavior.
		Description: "Normalize and validate a McDonald's receipt survey code, splitting it into the six McDVoice form fields (CN1-CN6). Call this first, on any code the user provides, before attempting a submission.",
	}, parseReceiptCode)
}

// -----------------------------------------------------------------------------
// Step 1.3: the submission tool.
// -----------------------------------------------------------------------------

type SubmitSurveyArgs struct {
	Chunks              []string `json:"chunks" jsonschema:"The six form fields CN1-CN6, exactly as returned by parse_receipt_code. Never construct these yourself."`
	VisitType           string   `json:"visit_type" jsonschema:"How the customer ordered: Drive-Thru, Dine-In, or Carry-Out."`
	OverallSatisfaction int      `json:"overall_satisfaction" jsonschema:"The customer's overall satisfaction from 1 (highly dissatisfied) to 5 (highly satisfied). Use only a rating the user actually stated; do not invent one."`
	Comment             string   `json:"comment,omitempty" jsonschema:"Optional verbatim comment supplied by the user. Leave empty if the user gave none."`
}

type SubmitSurveyResult struct {
	Submitted      bool     `json:"submitted" jsonschema:"Whether the survey run completed."`
	Mode           string   `json:"mode" jsonschema:"Either 'simulation' or 'live', describing whether a real submission occurred."`
	ValidationCode string   `json:"validation_code" jsonschema:"The validation code produced by the run."`
	SurveyURL      string   `json:"survey_url" jsonschema:"Where a human would complete this survey themselves."`
	Steps          []string `json:"steps" jsonschema:"The ordered steps the survey flow would perform."`
	Notice         string   `json:"notice" jsonschema:"A caveat that MUST be repeated to the user verbatim in your final answer."`
}

// Note: agent.ToolContext embeds context.Context (via ReadonlyContext), so it
// is passed straight to the HTTP calls. Cancelling the agent run cancels the
// survey walk.
func submitMcDVoiceSurvey(ctx agent.ToolContext, args SubmitSurveyArgs) (SubmitSurveyResult, error) {
	// Validate what the model handed us. A tool must never trust its arguments:
	// they are generated text, and a model that skipped parse_receipt_code will
	// happily pass six plausible-looking chunks it made up.
	if len(args.Chunks) != len(mcdVoiceChunkSizes) {
		return SubmitSurveyResult{}, fmt.Errorf("expected %d chunks from parse_receipt_code, got %d", len(mcdVoiceChunkSizes), len(args.Chunks))
	}
	var digits strings.Builder
	for i, chunk := range args.Chunks {
		if len(chunk) != mcdVoiceChunkSizes[i] {
			return SubmitSurveyResult{}, fmt.Errorf("chunk %d (CN%d) is %d digits, expected %d", i, i+1, len(chunk), mcdVoiceChunkSizes[i])
		}
		if nonDigitsRe.MatchString(chunk) {
			return SubmitSurveyResult{}, fmt.Errorf("chunk %d (CN%d) contains non-digit characters", i, i+1)
		}
		digits.WriteString(chunk)
	}
	if args.OverallSatisfaction < 1 || args.OverallSatisfaction > 5 {
		return SubmitSurveyResult{}, fmt.Errorf("overall_satisfaction must be 1-5, got %d", args.OverallSatisfaction)
	}

	visitType := normalizeVisitType(args.VisitType)

	if liveEnabled() {
		return submitLive(ctx, args, visitType)
	}
	return submitSimulated(args, visitType, digits.String())
}

// submitLive drives the real survey. The ToolContext carries the cancellation
// that bounds the whole walk, so a client disconnect stops it mid-survey.
func submitLive(ctx context.Context, args SubmitSurveyArgs, visitType string) (SubmitSurveyResult, error) {
	code, transcript, err := submitLiveSurvey(ctx, args.Chunks, visitType, args.OverallSatisfaction, args.Comment)
	if err != nil {
		// The transcript is attached to the FAILURE, not just the success. A
		// live walk that dies on question 7 is only fixable if you can see the
		// six pages that worked.
		return SubmitSurveyResult{
			Submitted: false,
			Mode:      "live",
			SurveyURL: mcdVoiceSurveyURL,
			Steps:     transcript,
			Notice: "The live survey did not complete, so NO validation code was obtained. " +
				"The receipt code may still be unused, or may have been consumed partway through — " +
				"check by opening " + mcdVoiceSurveyURL + " and entering it manually. Error: " + err.Error(),
		}, nil
	}
	return SubmitSurveyResult{
		Submitted:      true,
		Mode:           "live",
		ValidationCode: code,
		SurveyURL:      mcdVoiceSurveyURL,
		Steps:          transcript,
		Notice: "This is a REAL validation code from mcdvoice.com. Write it on your receipt. " +
			"The receipt code has now been used and cannot be submitted again.",
	}, nil
}

// submitSimulated returns a placeholder without touching the network.
func submitSimulated(args SubmitSurveyArgs, visitType, digits string) (SubmitSurveyResult, error) {
	code := simulatedValidationCode(digits, visitType, args.OverallSatisfaction)

	steps := []string{
		"Open " + mcdVoiceSurveyURL,
		fmt.Sprintf("Enter CN1-CN6: %s", strings.Join(args.Chunks, " ")),
		fmt.Sprintf("Select visit type: %s", visitType),
		fmt.Sprintf("Answer overall satisfaction: %d/5", args.OverallSatisfaction),
	}
	if args.Comment != "" {
		steps = append(steps, "Submit verbatim comment: "+args.Comment)
	}
	steps = append(steps, "Read the validation code off the confirmation page")

	return SubmitSurveyResult{
		Submitted:      true,
		Mode:           "simulation",
		ValidationCode: code,
		SurveyURL:      mcdVoiceSurveyURL,
		Steps:          steps,
		Notice: "This code is SIMULATED and will NOT be honored at a restaurant. No survey was " +
			"submitted. Set MCDVOICE_LIVE=1 to really complete the survey and get a usable code.",
	}, nil
}

const mcdVoiceSurveyURL = "https://www.mcdvoice.com/"

// normalizeVisitType maps free text onto the three options the real form
// offers, defaulting rather than failing: the visit type is incidental to the
// lesson and a wrong guess here should not abort a run.
func normalizeVisitType(s string) string {
	switch t := strings.ToLower(strings.TrimSpace(s)); {
	case strings.Contains(t, "dine"), strings.Contains(t, "eat in"), strings.Contains(t, "ate in"), strings.Contains(t, "inside"):
		return "Dine-In"
	case strings.Contains(t, "carry"), strings.Contains(t, "take"), strings.Contains(t, "to go"):
		return "Carry-Out"
	case t == "":
		return "Drive-Thru"
	default:
		return "Drive-Thru"
	}
}

// simulatedValidationCode derives a stable 5-digit code from the inputs.
//
// Deterministic on purpose: the same receipt yields the same code every run, so
// the agent's behavior is reproducible when you are debugging it. FNV is used
// because it is in the standard library and the value needs no cryptographic
// property — it is a placeholder, not a secret.
func simulatedValidationCode(digits, visitType string, satisfaction int) string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|%s|%d", digits, visitType, satisfaction)
	return fmt.Sprintf("%05d", h.Sum32()%100000)
}

func newSubmitSurveyTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "submit_mcdvoice_survey",
		Description: "Run the McDVoice survey flow for an already-validated receipt code and return the validation code. Only call this after parse_receipt_code reported valid=true.",
	}, submitMcDVoiceSurvey)
}

// -----------------------------------------------------------------------------
// Step 1.4: assemble the agent.
// -----------------------------------------------------------------------------

// mcDonaldsInstruction is the system prompt. Most of the work of making a
// tool-using agent reliable happens here, not in the tool code: the model needs
// to be told the call ORDER, what to do on failure, and what it is forbidden to
// fabricate.
const mcDonaldsInstruction = `You are the McDonald's Receipt Surveyer, an agent that processes McDonald's receipt survey codes.

Follow this procedure exactly:
1. Call parse_receipt_code with the user's code exactly as they wrote it.
2. If the result has valid=false, STOP. Explain the "reason" field to the user in plain language and tell them how to find the correct 26-digit code on their receipt. Do not call any other tool, and do not guess at a corrected code.
3. If valid=true, call submit_mcdvoice_survey, passing the "chunks" array through unchanged.
4. Report the validation code from the tool result.

Hard rules:
- Never invent, guess, or alter a receipt code or a validation code. Only ever report a validation_code that a tool returned to you.
- Use the satisfaction rating given to you in the request. Do not substitute your own judgment for it.
- Always repeat the tool's "notice" field to the user verbatim. It states whether the code is real or simulated, and the user needs to know which.
- If submit_mcdvoice_survey returns submitted=false, report that no code was obtained and relay the notice. Never present a code that is not in the tool result.

Be brief and concrete. No preamble.`

// buildMcDonaldsAgent constructs the Module 1 agent.
func buildMcDonaldsAgent(ctx context.Context) (agent.Agent, error) {
	model, err := newGeminiModel(ctx)
	if err != nil {
		return nil, err
	}

	parseTool, err := newParseReceiptCodeTool()
	if err != nil {
		return nil, fmt.Errorf("building parse_receipt_code: %w", err)
	}
	submitTool, err := newSubmitSurveyTool()
	if err != nil {
		return nil, fmt.Errorf("building submit_mcdvoice_survey: %w", err)
	}

	return llmagent.New(llmagent.Config{
		Name:        "mcdonalds_receipt_surveyer",
		Model:       model,
		Description: "Validates McDonald's receipt survey codes and walks the McDVoice survey flow.",
		Instruction: mcDonaldsInstruction,
		Tools:       []tool.Tool{parseTool, submitTool},
	})
}

// RunMcDonaldsAgent is called by POST /api/agents/mcdonalds-receipt-surveyer/run.
func RunMcDonaldsAgent(ctx context.Context, input map[string]any) (map[string]any, error) {
	rawCode := stringField(input, "survey_code")
	if rawCode == "" {
		return nil, fmt.Errorf("%w: survey_code is required", ErrInvalidInput)
	}
	// Deliberately loose: anything vaguely code-shaped is handed to the agent so
	// parse_receipt_code can produce a specific explanation. Validating
	// strictly here would make the agent's own error path unreachable.
	if digits := nonDigitsRe.ReplaceAllString(rawCode, ""); len(digits) < 1 || len(digits) > 40 {
		return nil, fmt.Errorf("%w: survey_code must contain between 1 and 40 digits", ErrInvalidInput)
	}

	visitType := stringField(input, "visit_type")
	if visitType == "" {
		visitType = "Drive-Thru"
	}

	a, err := buildMcDonaldsAgent(ctx)
	if err != nil {
		return nil, err
	}

	// The rating comes from configuration, not from the model. MCDVOICE_RATING
	// is the user's standing satisfaction; a per-request "rating" field
	// overrides it.
	rating := standingRating()
	if r := stringField(input, "rating"); r != "" {
		if n, err := strconv.Atoi(r); err == nil && n >= 1 && n <= 5 {
			rating = n
		}
	}

	prompt := fmt.Sprintf(
		"Process this McDonald's receipt survey code: %s\nVisit type: %s\nOverall satisfaction to submit: %d out of 5",
		rawCode, visitType, rating)
	if comment := stringField(input, "comment"); comment != "" {
		prompt += "\nCustomer comment: " + comment
	}

	trace, err := runAgentOnce(ctx, "mcdonalds_receipt_surveyer", a, prompt)
	if err != nil {
		return nil, err
	}

	mode := "simulation"
	if liveEnabled() {
		mode = "live"
	}
	out := map[string]any{
		"agent":        "mcdonalds-receipt-surveyer",
		"mode":         mode,
		"rating":       rating,
		"model":        ModelName(),
		"visit_type":   normalizeVisitType(visitType),
		"summary":      trace.FinalText,
		"tool_calls":   trace.ToolCalls,
		"processed_at": time.Now().UTC().Format(time.RFC3339),
	}

	// Prefer the tool's own output over the model's prose. The validation code
	// is a fact produced by code, so it is read from the tool result rather
	// than parsed back out of the model's sentence.
	if parsed := trace.result("parse_receipt_code"); parsed != nil {
		out["normalized_code"] = parsed["normalized_digits"]
		out["code_valid"] = parsed["valid"]
		if reason, ok := parsed["reason"].(string); ok && reason != "" {
			out["invalid_reason"] = reason
		}
	}
	if submitted := trace.result("submit_mcdvoice_survey"); submitted != nil {
		out["validation_code"] = submitted["validation_code"]
		out["notice"] = submitted["notice"]
		out["steps"] = submitted["steps"]
	}
	return out, nil
}

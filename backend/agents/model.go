package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// DefaultModelName is the Gemini model the lab agents use when GEMINI_MODEL is
// unset.
//
// Picking this is not purely a taste question:
//   - gemini-2.5-flash (what the original guide named) now returns 404 for API
//     keys created after its deprecation, even though ListModels still lists it.
//   - The newest Flash models (gemini-3.8-flash, gemini-3.7-flash,
//     gemini-flash-latest) frequently return 503 UNAVAILABLE on the free tier.
//
// gemini-3.5-flash is the newest Flash that answers reliably. Override it with
// GEMINI_MODEL to experiment without editing code.
const DefaultModelName = "gemini-3.5-flash"

// ErrMissingAPIKey is returned when no Gemini credential is configured.
var ErrMissingAPIKey = errors.New("GOOGLE_API_KEY is not set")

// ModelName returns the Gemini model the lab agents should use.
func ModelName() string {
	if m := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); m != "" {
		return m
	}
	return DefaultModelName
}

// newGeminiModel wraps the Gemini API in ADK's model.LLM interface.
//
// The key is passed explicitly rather than left to the SDK's environment
// sniffing, so a missing key fails here with a usable message instead of
// surfacing later as a Vertex AI authentication error.
func newGeminiModel(ctx context.Context) (model.LLM, error) {
	apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("%w: get a free key at https://aistudio.google.com/apikey and export GOOGLE_API_KEY", ErrMissingAPIKey)
	}
	m, err := gemini.NewModel(ctx, ModelName(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini.NewModel(%q): %w", ModelName(), err)
	}
	return m, nil
}

// toolCall records one tool invocation the model made during a run. Returning
// these to the caller is what makes an agent auditable: the final prose is the
// model's claim, while these are what actually executed.
type toolCall struct {
	Name     string         `json:"name"`
	Args     map[string]any `json:"args,omitempty"`
	Response map[string]any `json:"response,omitempty"`
}

// agentTrace is the result of a single-turn agent run.
type agentTrace struct {
	FinalText string
	ToolCalls []toolCall
}

// result returns the response payload of the last call to the named tool, or
// nil when the model never called it.
func (t agentTrace) result(toolName string) map[string]any {
	for i := len(t.ToolCalls) - 1; i >= 0; i-- {
		if t.ToolCalls[i].Name == toolName && t.ToolCalls[i].Response != nil {
			return t.ToolCalls[i].Response
		}
	}
	return nil
}

// runAgentOnce drives a single user message through an agent and collects both
// the final text and every tool call along the way.
//
// Sessions are in-memory and discarded when this returns, which is what makes
// the HTTP handler stateless: each POST is an independent conversation. Wiring
// a persistent session.Service here is what would turn these into multi-turn
// chats.
func runAgentOnce(ctx context.Context, appName string, a agent.Agent, prompt string) (agentTrace, error) {
	var trace agentTrace

	sessions := session.InMemoryService()
	created, err := sessions.Create(ctx, &session.CreateRequest{AppName: appName, UserID: "api_user"})
	if err != nil {
		return trace, fmt.Errorf("creating session: %w", err)
	}

	r, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          a,
		SessionService: sessions,
	})
	if err != nil {
		return trace, fmt.Errorf("runner.New: %w", err)
	}

	msg := genai.NewContentFromText(prompt, genai.RoleUser)

	// Pending function calls keyed by the ID Gemini assigns, so each response
	// can be paired with the arguments that produced it. Calls and responses
	// arrive in separate events.
	pending := map[string]*toolCall{}
	var allText, finalText strings.Builder

	for event, err := range r.Run(ctx, "api_user", created.Session.ID(), msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeNone,
	}) {
		if err != nil {
			return trace, fmt.Errorf("agent run failed: %w", err)
		}
		if event.LLMResponse.Content == nil || event.LLMResponse.Partial {
			continue
		}
		for _, part := range event.LLMResponse.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				call := &toolCall{Name: part.FunctionCall.Name, Args: part.FunctionCall.Args}
				trace.ToolCalls = append(trace.ToolCalls, *call)
				pending[part.FunctionCall.ID] = &trace.ToolCalls[len(trace.ToolCalls)-1]
			case part.FunctionResponse != nil:
				if call, ok := pending[part.FunctionResponse.ID]; ok {
					call.Response = part.FunctionResponse.Response
					delete(pending, part.FunctionResponse.ID)
					break
				}
				// No matching call ID: record the response on its own rather
				// than dropping it.
				trace.ToolCalls = append(trace.ToolCalls, toolCall{
					Name:     part.FunctionResponse.Name,
					Response: part.FunctionResponse.Response,
				})
			case part.Text != "":
				allText.WriteString(part.Text)
				if event.IsFinalResponse() {
					finalText.WriteString(part.Text)
				}
			}
		}
	}

	trace.FinalText = strings.TrimSpace(finalText.String())
	if trace.FinalText == "" {
		trace.FinalText = strings.TrimSpace(allText.String())
	}
	return trace, nil
}

// Command adk-playground is the Module 0 scratchpad for Google's Agent
// Development Kit (ADK) for Go.
//
// Docs: https://adk.dev/ (select "Go")
// Package reference: https://pkg.go.dev/google.golang.org/adk
//
// Before starting:
//  1. cd backend
//  2. export GOOGLE_API_KEY="your-key-from-aistudio.google.com"
//
// How to run:
//
//	go run ./cmd/adk-playground              # interactive CLI chat (default)
//	go run ./cmd/adk-playground web webui    # ADK's local web debugger on :8080
//	go run ./cmd/adk-playground --help       # every launcher mode and flag
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ryklin2/omsteaddAgents/backend/internal/envfile"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"
)

// modelName is the Gemini model every lab agent starts from. Flash is the
// cheapest current model that still does tool calling well, which is what
// Modules 1-3 need. Note gemini-2.5-flash is retired for new API keys, and
// the newest Flash models are often capacity-constrained.
const modelName = "gemini-3.5-flash"

func main() {
	ctx := context.Background()

	// Load backend/.env before reading any configuration. Shell variables
	// already set take precedence over the file.
	envResult, err := envfile.Load()
	if err != nil {
		log.Fatalf("failed to load .env: %v", err)
	}
	log.Printf("configuration: %s", envResult.Summary())

	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		log.Fatal("GOOGLE_API_KEY is not set. Get a free key at https://aistudio.google.com/apikey, then:\n" +
			`  export GOOGLE_API_KEY="your-key-here"`)
	}

	// Module 0.2: wrap the Gemini API in ADK's model.LLM interface. Passing the
	// key explicitly (rather than letting the SDK read the environment) keeps
	// the failure above as the only place a missing key is reported.
	model, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatalf("gemini.NewModel(%q) failed: %v", modelName, err)
	}

	// Module 0.3: the simplest useful agent — a model plus an instruction, and
	// no tools yet. Name is what the web UI and the REST API address it by, so
	// it has to be a valid identifier.
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:        "playground_agent",
		Model:       model,
		Description: "A sandbox agent for learning Google ADK for Go.",
		Instruction: "You are a concise, friendly tutor for Google's Agent Development Kit (ADK) for Go. " +
			"Answer questions about building agents in Go. When you are unsure about an API, say so and " +
			"point the user at the relevant page on https://adk.dev/ rather than inventing a function signature.",
	})
	if err != nil {
		log.Fatalf("llmagent.New() failed: %v", err)
	}

	// The launcher owns the run loop. `full` bundles every mode ADK ships —
	// console chat, the web debugger, the REST API, A2A — and picks one from
	// os.Args. With no arguments it defaults to the console.
	//
	// NewSingleLoader exposes exactly one agent; Modules 1-3 can swap in a
	// loader that serves several.
	cfg := &launcher.Config{
		AgentLoader: agent.NewSingleLoader(rootAgent),
	}

	if err := full.NewLauncher().Execute(ctx, cfg, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "launcher failed: %v\n", err)
		os.Exit(1)
	}
}

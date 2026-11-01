package main

import (
	"fmt"
	"os"
)

// =============================================================================
// MODULE 0: Google ADK for Go Quickstart Playground
//
// Docs: https://adk.dev/ (select "Go")
// Package reference: https://pkg.go.dev/google.golang.org/adk
//
// Before starting:
//   1. cd backend
//   2. go get google.golang.org/adk@latest google.golang.org/genai@latest
//   3. export GOOGLE_API_KEY="your-key-from-aistudio.google.com"
//
// How to run:
//   go run ./cmd/adk-playground
// =============================================================================

func main() {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Tip: set GOOGLE_API_KEY in your environment before calling Gemini.")
	}

	// TODO(learn - Module 0.1): Import the ADK packages above:
	//   "context"
	//   "log"
	//   "google.golang.org/adk/agent"
	//   "google.golang.org/adk/agent/llmagent"
	//   "google.golang.org/adk/cmd/launcher"
	//   "google.golang.org/adk/cmd/launcher/full"
	//   "google.golang.org/adk/model/gemini"
	//   "google.golang.org/genai"

	// TODO(learn - Module 0.2): Initialize the Gemini model with gemini.NewModel:
	//   ctx := context.Background()
	//   model, err := gemini.NewModel(ctx, "gemini-2.5-flash", &genai.ClientConfig{
	//       APIKey: apiKey,
	//   })

	// TODO(learn - Module 0.3): Create your first llmagent.New(llmagent.Config{...})
	//   Give it a Name, Model, Description, and Instruction, then launch it with
	//   full.NewLauncher().Execute(ctx, config, os.Args[1:]) to try both CLI mode
	//   and ADK's built-in local web UI!

	fmt.Println("ADK Playground ready! Open backend/cmd/adk-playground/main.go and docs/ADK_LEARNING_GUIDE.md to begin Module 0.")
}


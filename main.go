package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hersh/gotris/internal/tui"
)

// This is the standalone single-player entry point.
// For multiplayer, use:
//   Server: go run ./cmd/server
//   Client: go run ./cmd/client --server ws://localhost:8080/ws --name YourName

func main() {
	name := "Player"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}

	// nil client = single-player only mode (no network)
	model := tui.NewModel(name, nil)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

package main

import (
	"flag"
	"fmt"
	"os"
	"os/user"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hersh/gotris/internal/netclient"
	"github.com/hersh/gotris/internal/tui"
)

func main() {
	serverAddr := flag.String("server", "ws://localhost:8080/ws", "WebSocket server address")
	playerName := flag.String("name", "", "Player name (defaults to OS username)")
	flag.Parse()

	name := *playerName
	if name == "" {
		if u, err := user.Current(); err == nil && u.Username != "" {
			name = u.Username
		} else {
			name = "Player"
		}
	}

	// Connect to server
	client, err := netclient.New(*serverAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to server at %s: %v\n", *serverAddr, err)
		fmt.Fprintf(os.Stderr, "Make sure the server is running (go run ./cmd/server)\n")
		os.Exit(1)
	}
	defer client.Close()

	// Create the bubbletea model
	model := tui.NewModel(name, client)

	// Create the program
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Wire the program into the client so readPump can send tea.Msgs
	client.SetProgram(p)
	client.Start()

	// Run the TUI (blocking)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

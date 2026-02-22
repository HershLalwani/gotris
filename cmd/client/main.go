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

// DefaultServer is the default server address.
// Override at build time with:
//
//	go build -ldflags "-X main.DefaultServer=https://your-app.railway.app" ./cmd/client
var DefaultServer = "http://localhost:8080"

func main() {
	serverAddr := flag.String("server", DefaultServer, "Server HTTP address")
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

	// Create the client (HTTP only at startup, no WS connection yet)
	client := netclient.New(*serverAddr)
	defer client.Close()

	// Create the bubbletea model
	model := tui.NewModel(name, client)

	// Create the program
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Wire the program into the client so WS readPump can send tea.Msgs
	client.SetProgram(p)

	// Run the TUI (blocking) — no server connection needed to start
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

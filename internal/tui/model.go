package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hersh/gotris/internal/game"
	"github.com/hersh/gotris/internal/netclient"
	"github.com/hersh/gotris/internal/protocol"
)

// --- Custom tea.Msg types ---

type TickMsg time.Time
type GameTickMsg time.Time
type CountdownMsg time.Time

// SnapshotTickMsg triggers sending board snapshots to the server.
type SnapshotTickMsg time.Time

// --- Screens and modes ---

type Screen int

const (
	ScreenConnecting Screen = iota
	ScreenWelcome
	ScreenLobby
	ScreenCountdown
	ScreenPlaying
	ScreenGameOver
)

type GameMode int

const (
	ModeNone GameMode = iota
	ModeSingle
	ModeMulti
)

// --- Model ---

type Model struct {
	screen     Screen
	mode       GameMode
	playerID   string
	playerName string
	gameState  *game.GameState
	width      int
	height     int
	countdown  int

	// Network
	client *netclient.Client

	// Lobby state (from server)
	lobbyPlayers []protocol.LobbyPlayer

	// Multiplayer state
	opponents    []protocol.OpponentState
	seed         int64
	matchPlayers []string
	ready        bool
	matchResult  *protocol.MatchOverPayload

	// Error
	err          error
	disconnected bool
}

// NewModel creates a model for the client TUI.
// If client is nil, only single-player mode is available.
func NewModel(playerName string, client *netclient.Client) Model {
	screen := ScreenConnecting
	if client == nil {
		screen = ScreenWelcome
	}
	return Model{
		screen:     screen,
		playerName: playerName,
		client:     client,
		ready:      false,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func gameTickCmd(speed time.Duration) tea.Cmd {
	return tea.Tick(speed, func(t time.Time) tea.Msg {
		return GameTickMsg(t)
	})
}

func countdownCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return CountdownMsg(t)
	})
}

func snapshotTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return SnapshotTickMsg(t)
	})
}

// --- Update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case TickMsg:
		return m.handleTick()
	case GameTickMsg:
		return m.handleGameTick()
	case CountdownMsg:
		return m.handleCountdown()
	case SnapshotTickMsg:
		return m.handleSnapshotTick()

	// Network messages
	case netclient.ConnectedMsg:
		return m.handleConnected(msg)
	case netclient.DisconnectedMsg:
		m.disconnected = true
		m.err = msg.Err
		return m, nil
	case netclient.ServerMsg:
		return m.handleServerMsg(msg)
	}
	return m, nil
}

// --- Network message handlers ---

func (m Model) handleConnected(msg netclient.ConnectedMsg) (tea.Model, tea.Cmd) {
	m.playerID = msg.PlayerID
	m.screen = ScreenWelcome
	return m, nil
}

func (m Model) handleServerMsg(msg netclient.ServerMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case protocol.MsgLobbyUpdate:
		var payload protocol.LobbyUpdatePayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			m.lobbyPlayers = payload.Players
		}

	case protocol.MsgCountdown:
		var payload protocol.CountdownPayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			m.countdown = payload.Value
			if m.screen != ScreenCountdown {
				m.screen = ScreenCountdown
			}
		}

	case protocol.MsgGameStart:
		var payload protocol.GameStartPayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			m.seed = payload.Seed
			m.matchPlayers = payload.Players
			m.matchResult = nil
			m.opponents = nil

			// Create seeded game state - local authority
			m.gameState = game.NewSeededGameState(m.playerID, m.playerName, m.seed)
			m.screen = ScreenPlaying

			return m, tea.Batch(
				gameTickCmd(m.gameState.GetDropSpeed()),
				snapshotTickCmd(),
			)
		}

	case protocol.MsgOpponentUpdate:
		var payload protocol.OpponentUpdatePayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			m.opponents = payload.Opponents
		}

	case protocol.MsgReceiveGarbage:
		var payload protocol.ReceiveGarbagePayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			if m.gameState != nil && !m.gameState.IsGameOver {
				// Buffer garbage - it applies on next piece lock
				m.gameState.ReceiveGarbage(payload.Lines)
			}
		}

	case protocol.MsgMatchOver:
		var payload protocol.MatchOverPayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			m.matchResult = &payload
			if payload.WinnerID == m.playerID && m.gameState != nil {
				m.gameState.IsWinner = true
			}
			m.screen = ScreenGameOver
		}
	}

	return m, nil
}

// --- Key handlers ---

func (m Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.client != nil {
			m.client.Close()
		}
		return m, tea.Quit
	case "q":
		if m.screen == ScreenPlaying {
			// Don't quit during gameplay with q
			break
		}
		if m.client != nil {
			m.client.Close()
		}
		return m, tea.Quit
	}

	switch m.screen {
	case ScreenWelcome:
		return m.handleWelcomeKeys(msg)
	case ScreenLobby:
		return m.handleLobbyKeys(msg)
	case ScreenPlaying:
		return m.handlePlayingKeys(msg)
	case ScreenGameOver:
		return m.handleGameOverKeys(msg)
	}
	return m, nil
}

func (m Model) handleWelcomeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1", "s":
		// Single player - local only, no network
		m.mode = ModeSingle
		m.screen = ScreenPlaying
		if m.playerID == "" {
			m.playerID = "local"
		}
		m.gameState = game.NewGameState(m.playerID, m.playerName)
		return m, gameTickCmd(m.gameState.GetDropSpeed())
	case "2", "enter":
		// Multiplayer - join the server lobby
		if m.client == nil {
			// No server connection - can't do multiplayer
			return m, nil
		}
		m.mode = ModeMulti
		m.screen = ScreenLobby
		m.ready = false
		if m.client != nil {
			m.client.Send(protocol.Envelope{
				Type: protocol.MsgJoin,
				Payload: protocol.JoinPayload{
					PlayerName: m.playerName,
				},
			})
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleLobbyKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case " ":
		m.ready = !m.ready
		if m.client != nil {
			m.client.Send(protocol.Envelope{
				Type: protocol.MsgReady,
				Payload: protocol.ReadyPayload{
					Ready: m.ready,
				},
			})
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handlePlayingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.gameState == nil || m.gameState.IsGameOver {
		return m, nil
	}

	switch msg.String() {
	case "left", "h":
		m.gameState.MoveLeft()
	case "right", "l":
		m.gameState.MoveRight()
	case "down", "j":
		m.gameState.MoveDown()
	case "up", "x":
		m.gameState.Rotate()
	case " ", "c":
		m.gameState.HardDrop()
		// After hard drop, check for attack
		m.sendAttackIfNeeded()
		m.checkLocalGameOver()
	case "z":
		m.gameState.Hold()
	}
	return m, nil
}

func (m Model) handleGameOverKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.mode == ModeSingle {
			m.screen = ScreenWelcome
			m.mode = ModeNone
			m.gameState = nil
		} else {
			// Return to lobby - wait for server lobby update
			m.screen = ScreenLobby
			m.ready = false
			m.matchResult = nil
			m.opponents = nil
			m.gameState = nil
		}
		return m, nil
	}
	return m, nil
}

// --- Tick handlers ---

func (m Model) handleTick() (tea.Model, tea.Cmd) {
	// Check for local game over in single player
	if m.mode == ModeSingle && m.gameState != nil && m.gameState.IsGameOver {
		m.screen = ScreenGameOver
	}
	return m, tickCmd()
}

func (m Model) handleGameTick() (tea.Model, tea.Cmd) {
	if m.screen != ScreenPlaying || m.gameState == nil || m.gameState.IsGameOver {
		return m, nil
	}

	m.gameState.Tick()

	// After tick, check if lines were cleared (attack)
	m.sendAttackIfNeeded()
	m.checkLocalGameOver()

	return m, gameTickCmd(m.gameState.GetDropSpeed())
}

func (m Model) handleCountdown() (tea.Model, tea.Cmd) {
	// Countdown is driven by the server via MsgCountdown messages.
	// This local tick is no longer used for countdown in multiplayer.
	return m, nil
}

func (m Model) handleSnapshotTick() (tea.Model, tea.Cmd) {
	if m.screen != ScreenPlaying || m.mode != ModeMulti || m.gameState == nil {
		return m, nil
	}

	// Send board snapshot to server
	if m.client != nil {
		m.client.Send(protocol.Envelope{
			Type: protocol.MsgBoardSnapshot,
			Payload: protocol.BoardSnapshotPayload{
				Score: m.gameState.Score,
				Level: m.gameState.Level,
				Lines: m.gameState.Lines,
				Alive: !m.gameState.IsGameOver,
				Board: m.gameState.Board.ToFlat(),
			},
		})
	}

	return m, snapshotTickCmd()
}

// sendAttackIfNeeded checks if the game state has accumulated attack power and sends it.
func (m *Model) sendAttackIfNeeded() {
	if m.mode != ModeMulti || m.gameState == nil || m.client == nil {
		return
	}
	if m.gameState.AttackPower > 0 {
		m.client.Send(protocol.Envelope{
			Type: protocol.MsgLinesCleared,
			Payload: protocol.LinesClearedPayload{
				Count:       m.gameState.AttackPower, // simplified: count = attack
				AttackPower: m.gameState.AttackPower,
			},
		})
		m.gameState.AttackPower = 0
	}
}

// checkLocalGameOver notifies the server when this player dies.
func (m *Model) checkLocalGameOver() {
	if m.mode != ModeMulti || m.gameState == nil || m.client == nil {
		return
	}
	if m.gameState.IsGameOver {
		m.client.Send(protocol.Envelope{
			Type:    protocol.MsgPlayerDead,
			Payload: protocol.PlayerDeadPayload{},
		})
	}
}

// --- View ---

func (m Model) View() string {
	if m.disconnected {
		return m.renderCentered("Disconnected from server.\nPress Ctrl+C to exit.")
	}

	switch m.screen {
	case ScreenConnecting:
		return m.renderCentered("Connecting to server...")
	case ScreenWelcome:
		return m.renderWelcome()
	case ScreenLobby:
		return m.renderLobby()
	case ScreenCountdown:
		return m.renderCountdown()
	case ScreenPlaying:
		return m.renderPlaying()
	case ScreenGameOver:
		return m.renderGameOver()
	}
	return ""
}

func (m Model) renderCentered(content string) string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(content)
}

func (m Model) renderWelcome() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(RenderWelcome())
}

func (m Model) renderLobby() string {
	names := make([]string, len(m.lobbyPlayers))
	readyMap := make(map[string]bool)

	for i, p := range m.lobbyPlayers {
		names[i] = p.Name
		readyMap[p.Name] = p.Ready
	}

	lobbyContent := RenderLobby(names, readyMap, m.playerName)

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(lobbyContent)
}

func (m Model) renderCountdown() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(RenderCountdown(m.countdown))
}

func (m Model) renderPlaying() string {
	if m.gameState == nil {
		return "Loading..."
	}

	board := RenderBoard(m.gameState, game.BoardWidth, game.BoardHeight)
	info := RenderInfo(m.gameState)

	leftPanel := lipgloss.NewStyle().
		Width(24).
		Render(info)

	centerPanel := lipgloss.NewStyle().
		Padding(1, 2).
		Render(board)

	mainContent := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftPanel,
		centerPanel,
	)

	if m.mode == ModeMulti && len(m.opponents) > 0 {
		opponentView := RenderNetOpponents(m.opponents, 8)
		if opponentView != "" {
			rightPanel := lipgloss.NewStyle().
				Padding(1, 2).
				Render(opponentView)
			mainContent = lipgloss.JoinHorizontal(
				lipgloss.Top,
				leftPanel,
				centerPanel,
				rightPanel,
			)
		}
	}

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(mainContent)
}

func (m Model) renderGameOver() string {
	if m.gameState == nil {
		return m.renderCentered("Game Over")
	}

	score := m.gameState.Score
	var content string

	if m.mode == ModeSingle {
		content = RenderSingleGameOver(score)
	} else if m.matchResult != nil {
		isWinner := m.matchResult.WinnerID == m.playerID
		content = RenderGameOver(isWinner, score, m.matchResult.YourRank)
	} else {
		isWinner := m.gameState.IsWinner
		rank := 0
		content = RenderGameOver(isWinner, score, rank)
	}
	content += "\n\nPress ENTER to continue"

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(content)
}

func (m Model) GetPlayerID() string {
	return m.playerID
}

// FormatPlayerList formats a list of players for display.
func FormatPlayerList(players []struct {
	ID      string
	Name    string
	Ready   bool
	IsAlive bool
}) string {
	var sb strings.Builder
	for _, p := range players {
		status := "[ ]"
		if p.Ready {
			status = "[✓]"
		}
		if !p.IsAlive {
			status = "[☠]"
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", status, p.Name))
	}
	return sb.String()
}

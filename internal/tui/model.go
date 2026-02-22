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
	ScreenMainMenu
	ScreenEditName
	ScreenJoinRoom
	ScreenListRooms
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

	// Room state
	roomCode       string
	roomInput      string
	nameInput      string
	roomError      string
	availableRooms []protocol.RoomInfo
	roomListCursor int
	roomListPage   int

	// Targeting
	targetID    string // "" = random, otherwise a player ID
	targetIndex int    // -1 = random, 0..N-1 = index into opponents
}

// NewModel creates a model for the client TUI.
// If client is nil, only single-player mode is available.
// The client no longer needs a WebSocket at startup; it connects on demand.
func NewModel(playerName string, client *netclient.Client) Model {
	return Model{
		screen:      ScreenMainMenu,
		playerName:  playerName,
		nameInput:   playerName,
		client:      client,
		ready:       false,
		targetIndex: -1,
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

	// HTTP response messages
	case netclient.RoomCreatedHTTPMsg:
		return m.handleRoomCreatedHTTP(msg)
	case netclient.RoomJoinedHTTPMsg:
		return m.handleRoomJoinedHTTP(msg)
	case netclient.RoomsListedMsg:
		return m.handleRoomsListed(msg)
	}
	return m, nil
}

// --- Network message handlers ---

func (m Model) handleConnected(msg netclient.ConnectedMsg) (tea.Model, tea.Cmd) {
	m.playerID = msg.PlayerID
	// Don't change screen here; the HTTP response handler already moved us to ScreenLobby
	return m, nil
}

func (m Model) handleRoomCreatedHTTP(msg netclient.RoomCreatedHTTPMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.roomError = msg.Err.Error()
		m.screen = ScreenMainMenu
		return m, nil
	}
	m.roomCode = msg.RoomID
	m.roomError = ""
	m.screen = ScreenLobby
	m.ready = false
	return m, nil
}

func (m Model) handleRoomJoinedHTTP(msg netclient.RoomJoinedHTTPMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.roomError = msg.Err.Error()
		if m.screen == ScreenConnecting {
			m.screen = ScreenJoinRoom
		}
		return m, nil
	}
	m.roomCode = msg.RoomID
	m.roomError = ""
	m.screen = ScreenLobby
	m.ready = false
	return m, nil
}

func (m Model) handleRoomsListed(msg netclient.RoomsListedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.roomError = msg.Err.Error()
		m.screen = ScreenMainMenu
		return m, nil
	}
	m.availableRooms = msg.Rooms
	m.roomError = ""
	m.roomListCursor = 0
	m.roomListPage = 0
	m.screen = ScreenListRooms
	return m, nil
}

// --- HTTP tea.Cmd helpers ---

func createRoomCmd(client *netclient.Client, playerName string) tea.Cmd {
	return func() tea.Msg {
		roomID, token, err := client.CreateRoom(playerName)
		if err != nil {
			return netclient.RoomCreatedHTTPMsg{Err: err}
		}
		if err := client.ConnectToRoom(roomID, token); err != nil {
			return netclient.RoomCreatedHTTPMsg{RoomID: roomID, Err: err}
		}
		return netclient.RoomCreatedHTTPMsg{RoomID: roomID, Token: token}
	}
}

func joinRoomHTTPCmd(client *netclient.Client, roomID, playerName string) tea.Cmd {
	return func() tea.Msg {
		token, err := client.JoinRoom(roomID, playerName)
		if err != nil {
			return netclient.RoomJoinedHTTPMsg{Err: err}
		}
		if err := client.ConnectToRoom(roomID, token); err != nil {
			return netclient.RoomJoinedHTTPMsg{RoomID: roomID, Err: err}
		}
		return netclient.RoomJoinedHTTPMsg{RoomID: roomID, Token: token}
	}
}

func listRoomsCmd(client *netclient.Client) tea.Cmd {
	return func() tea.Msg {
		rooms, err := client.ListRooms()
		return netclient.RoomsListedMsg{Rooms: rooms, Err: err}
	}
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
			// Only transition to countdown from lobby/countdown screens.
			// Ignore late countdown messages if we're already playing.
			if m.screen == ScreenLobby || m.screen == ScreenCountdown {
				m.countdown = payload.Value
				m.screen = ScreenCountdown
			}
		}

	case protocol.MsgGameStart:
		var payload protocol.GameStartPayload
		if json.Unmarshal(msg.Raw, &payload) == nil {
			m.seed = payload.Seed
			m.matchPlayers = payload.Players
			m.matchResult = nil
			// Don't clear m.opponents here — keep stale data until
			// the first MsgOpponentUpdate arrives, preventing a layout
			// shift where the opponent panel vanishes then reappears.

			// Reset targeting
			m.targetID = ""
			m.targetIndex = -1

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
	case ScreenMainMenu:
		return m.handleMainMenuKeys(msg)
	case ScreenEditName:
		return m.handleEditNameKeys(msg)
	case ScreenJoinRoom:
		return m.handleJoinRoomKeys(msg)
	case ScreenListRooms:
		return m.handleListRoomsKeys(msg)
	case ScreenLobby:
		return m.handleLobbyKeys(msg)
	case ScreenPlaying:
		return m.handlePlayingKeys(msg)
	case ScreenGameOver:
		return m.handleGameOverKeys(msg)
	}
	return m, nil
}

func (m Model) handleMainMenuKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "2":
		// Create a room via HTTP, then connect WS
		if m.client == nil {
			return m, nil
		}
		m.mode = ModeMulti
		m.screen = ScreenConnecting
		m.roomError = ""
		return m, createRoomCmd(m.client, m.playerName)
	case "3":
		// Join a room by code
		if m.client == nil {
			return m, nil
		}
		m.mode = ModeMulti
		m.screen = ScreenJoinRoom
		m.roomInput = ""
		m.roomError = ""
		return m, nil
	case "4":
		// Browse rooms
		if m.client == nil {
			return m, nil
		}
		m.screen = ScreenConnecting
		m.roomError = ""
		return m, listRoomsCmd(m.client)
	case "5":
		// Edit name
		m.screen = ScreenEditName
		m.nameInput = m.playerName
		return m, nil
	}
	return m, nil
}

func (m Model) handleEditNameKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput)
		if name != "" {
			m.playerName = name
		}
		m.screen = ScreenMainMenu
		return m, nil
	case "esc":
		m.screen = ScreenMainMenu
		return m, nil
	case "backspace":
		if len(m.nameInput) > 0 {
			m.nameInput = m.nameInput[:len(m.nameInput)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 && len(m.nameInput) < 20 {
			m.nameInput += msg.String()
		}
		return m, nil
	}
}

func (m Model) handleJoinRoomKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		code := strings.TrimSpace(m.roomInput)
		if code != "" && m.client != nil {
			m.screen = ScreenConnecting
			return m, joinRoomHTTPCmd(m.client, code, m.playerName)
		}
		return m, nil
	case "esc":
		m.screen = ScreenMainMenu
		m.roomInput = ""
		m.roomError = ""
		return m, nil
	case "backspace":
		if len(m.roomInput) > 0 {
			m.roomInput = m.roomInput[:len(m.roomInput)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 && len(m.roomInput) < 5 {
			m.roomInput += strings.ToUpper(msg.String())
		}
		return m, nil
	}
}

func (m Model) handleListRoomsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	const roomsPerPage = 10
	totalRooms := len(m.availableRooms)
	totalPages := (totalRooms + roomsPerPage - 1) / roomsPerPage
	if totalPages < 1 {
		totalPages = 1
	}
	pageStart := m.roomListPage * roomsPerPage
	pageEnd := pageStart + roomsPerPage
	if pageEnd > totalRooms {
		pageEnd = totalRooms
	}
	pageCount := pageEnd - pageStart

	switch msg.String() {
	case "esc":
		m.screen = ScreenMainMenu
		m.availableRooms = nil
		m.roomError = ""
		return m, nil
	case "r":
		// Refresh room list
		if m.client != nil {
			m.screen = ScreenConnecting
			return m, listRoomsCmd(m.client)
		}
		return m, nil
	case "up", "k":
		if m.roomListCursor > 0 {
			m.roomListCursor--
		} else if m.roomListPage > 0 {
			// Wrap to bottom of previous page
			m.roomListPage--
			newStart := m.roomListPage * roomsPerPage
			newEnd := newStart + roomsPerPage
			if newEnd > totalRooms {
				newEnd = totalRooms
			}
			m.roomListCursor = newEnd - newStart - 1
		}
		return m, nil
	case "down", "j":
		if m.roomListCursor < pageCount-1 {
			m.roomListCursor++
		} else if m.roomListPage < totalPages-1 {
			// Wrap to top of next page
			m.roomListPage++
			m.roomListCursor = 0
		}
		return m, nil
	case "left", "h":
		if m.roomListPage > 0 {
			m.roomListPage--
			m.roomListCursor = 0
		}
		return m, nil
	case "right", "l":
		if m.roomListPage < totalPages-1 {
			m.roomListPage++
			m.roomListCursor = 0
			// Clamp cursor to new page size
			newStart := m.roomListPage * roomsPerPage
			newEnd := newStart + roomsPerPage
			if newEnd > totalRooms {
				newEnd = totalRooms
			}
			if m.roomListCursor >= newEnd-newStart {
				m.roomListCursor = newEnd - newStart - 1
			}
		}
		return m, nil
	case "enter":
		if totalRooms > 0 && m.client != nil {
			idx := pageStart + m.roomListCursor
			if idx < totalRooms {
				room := m.availableRooms[idx]
				if room.Phase != "lobby" {
					m.roomError = "Cannot join: game already in progress"
					return m, nil
				}
				m.mode = ModeMulti
				m.screen = ScreenConnecting
				m.roomError = ""
				return m, joinRoomHTTPCmd(m.client, room.RoomID, m.playerName)
			}
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
	case "esc":
		// Leave the room: disconnect WebSocket (server handles cleanup)
		if m.client != nil {
			m.client.DisconnectFromRoom()
		}
		m.screen = ScreenMainMenu
		m.roomCode = ""
		m.ready = false
		m.lobbyPlayers = nil
		m.disconnected = false
		m.err = nil
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
	case "tab":
		m.cycleTarget()
	}
	return m, nil
}

func (m Model) handleGameOverKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.mode == ModeSingle {
			m.screen = ScreenMainMenu
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
		return m, tickCmd()
	case "esc":
		// Leave the room and return to main menu
		if m.client != nil && m.mode == ModeMulti {
			m.client.DisconnectFromRoom()
		}
		m.screen = ScreenMainMenu
		m.mode = ModeNone
		m.roomCode = ""
		m.ready = false
		m.matchResult = nil
		m.opponents = nil
		m.gameState = nil
		m.disconnected = false
		m.err = nil
		return m, tickCmd()
	}
	return m, nil
}

// --- Tick handlers ---

func (m Model) handleTick() (tea.Model, tea.Cmd) {
	// During gameplay/countdown/gameover, don't reschedule the general tick.
	// Game ticks, snapshot ticks, and server messages handle those screens.
	if m.screen == ScreenPlaying || m.screen == ScreenCountdown || m.screen == ScreenGameOver {
		return m, nil
	}
	return m, tickCmd()
}

func (m Model) handleGameTick() (tea.Model, tea.Cmd) {
	if m.screen != ScreenPlaying || m.gameState == nil {
		return m, nil
	}

	// Check for game over
	if m.gameState.IsGameOver {
		if m.mode == ModeSingle {
			m.screen = ScreenGameOver
		}
		// For multiplayer, wait for MsgMatchOver from the server.
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
		connMsg := "Connecting..."
		if m.roomError != "" {
			connMsg = m.roomError
		}
		return m.renderCentered(connMsg)
	case ScreenMainMenu:
		return m.renderMainMenu()
	case ScreenEditName:
		return m.renderEditName()
	case ScreenJoinRoom:
		return m.renderJoinRoom()
	case ScreenListRooms:
		return m.renderListRooms()
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

func (m Model) renderMainMenu() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(RenderMainMenu(m.playerName))
}

func (m Model) renderEditName() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(RenderEditName(m.nameInput))
}

func (m Model) renderJoinRoom() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(RenderJoinRoom(m.roomInput, m.roomError))
}

func (m Model) renderListRooms() string {
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(RenderListRooms(m.availableRooms, m.roomError, m.roomListCursor, m.roomListPage))
}

func (m Model) renderLobby() string {
	lobbyContent := RenderLobby(m.lobbyPlayers, m.playerID, m.roomCode)

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

	// Build target name for info panel
	targetName := ""
	if m.mode == ModeMulti {
		if m.targetID == "" {
			targetName = "Random"
		} else {
			for _, opp := range m.opponents {
				if opp.PlayerID == m.targetID {
					targetName = opp.PlayerName
					break
				}
			}
			if targetName == "" {
				targetName = "Random" // target left, reset display
			}
		}
	}

	info := RenderInfo(m.gameState, targetName)

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
		opponentView := RenderNetOpponents(m.opponents, 8, m.targetID)
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

// cycleTarget cycles the attack target: random → opponent 0 → opponent 1 → ... → random.
func (m *Model) cycleTarget() {
	if m.mode != ModeMulti || len(m.opponents) == 0 {
		return
	}

	// Count alive opponents
	var aliveIDs []string
	for _, opp := range m.opponents {
		if opp.Alive {
			aliveIDs = append(aliveIDs, opp.PlayerID)
		}
	}
	if len(aliveIDs) == 0 {
		m.targetID = ""
		m.targetIndex = -1
		return
	}

	// Find current position in alive list
	currentPos := -1 // -1 = random
	for i, id := range aliveIDs {
		if id == m.targetID {
			currentPos = i
			break
		}
	}

	// Advance to next
	nextPos := currentPos + 1
	if nextPos >= len(aliveIDs) {
		// Wrap back to random
		m.targetID = ""
		m.targetIndex = -1
	} else {
		m.targetID = aliveIDs[nextPos]
		// Find the index in the full opponents list for rendering
		for i, opp := range m.opponents {
			if opp.PlayerID == m.targetID {
				m.targetIndex = i
				break
			}
		}
	}

	// Notify the server
	if m.client != nil {
		m.client.Send(protocol.Envelope{
			Type: protocol.MsgSetTarget,
			Payload: protocol.SetTargetPayload{
				TargetID: m.targetID,
			},
		})
	}
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

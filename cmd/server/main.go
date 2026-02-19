package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hersh/gotris/internal/protocol"
)

// --- Configuration ---

const (
	defaultPort       = "8080"
	broadcastInterval = 100 * time.Millisecond
	writeWait         = 10 * time.Second
	pongWait          = 60 * time.Second
	pingInterval      = (pongWait * 9) / 10
	maxMessageSize    = 16384
	minPlayers        = 2
)

// --- Upgrader ---

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// --- Player (server-side) ---

type Player struct {
	ID     string
	Name   string
	Ready  bool
	Alive  bool
	Conn   *websocket.Conn
	sendCh chan []byte
	// Latest snapshot from this client
	mu       sync.Mutex
	Snapshot *protocol.BoardSnapshotPayload
}

func newPlayer(id string, conn *websocket.Conn) *Player {
	return &Player{
		ID:     id,
		Conn:   conn,
		Alive:  true,
		sendCh: make(chan []byte, 256),
	}
}

// writePump sends messages from sendCh to the WebSocket.
func (p *Player) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		p.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-p.sendCh:
			p.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				p.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := p.Conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			p.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := p.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// send marshals an envelope and queues it.
func (p *Player) send(env protocol.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("marshal error for player %s: %v", p.ID, err)
		return
	}
	select {
	case p.sendCh <- data:
	default:
		log.Printf("send channel full for player %s, dropping message", p.ID)
	}
}

// --- Match ---

type MatchPhase int

const (
	PhaseLobby MatchPhase = iota
	PhaseCountdown
	PhasePlaying
	PhaseGameOver
)

type Match struct {
	mu        sync.RWMutex
	id        string
	phase     MatchPhase
	players   map[string]*Player
	seed      int64
	countdown int
	winnerID  string
	stopCh    chan struct{}
}

func newMatch(id string) *Match {
	return &Match{
		id:      id,
		phase:   PhaseLobby,
		players: make(map[string]*Player),
		stopCh:  make(chan struct{}),
	}
}

func (m *Match) addPlayer(p *Player) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.players[p.ID] = p
}

func (m *Match) removePlayer(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.players[id]; ok {
		close(p.sendCh)
		delete(m.players, id)
	}

	// If we're playing and a player leaves, mark them dead
	if m.phase == PhasePlaying {
		m.checkWinCondition()
	}
}

func (m *Match) playerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.players)
}

func (m *Match) broadcastLobbyUpdate() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var players []protocol.LobbyPlayer
	for _, p := range m.players {
		players = append(players, protocol.LobbyPlayer{
			PlayerID: p.ID,
			Name:     p.Name,
			Ready:    p.Ready,
		})
	}

	env := protocol.Envelope{
		Type:    protocol.MsgLobbyUpdate,
		Payload: protocol.LobbyUpdatePayload{Players: players},
	}

	for _, p := range m.players {
		p.send(env)
	}
}

func (m *Match) canStart() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.players) < minPlayers {
		return false
	}
	for _, p := range m.players {
		if !p.Ready {
			return false
		}
	}
	return true
}

func (m *Match) startCountdown() {
	m.mu.Lock()
	m.phase = PhaseCountdown
	m.countdown = 3
	m.mu.Unlock()

	go func() {
		for i := 3; i > 0; i-- {
			m.mu.Lock()
			m.countdown = i
			m.mu.Unlock()

			m.broadcastToAll(protocol.Envelope{
				Type:    protocol.MsgCountdown,
				Payload: protocol.CountdownPayload{Value: i},
			})
			time.Sleep(time.Second)
		}
		m.startGame()
	}()
}

func (m *Match) startGame() {
	m.mu.Lock()
	m.phase = PhasePlaying
	m.seed = rand.Int63()
	m.winnerID = ""

	var playerIDs []string
	for id, p := range m.players {
		playerIDs = append(playerIDs, id)
		p.Alive = true
		p.Ready = false
		p.mu.Lock()
		p.Snapshot = nil
		p.mu.Unlock()
	}
	m.mu.Unlock()

	m.broadcastToAll(protocol.Envelope{
		Type: protocol.MsgGameStart,
		Payload: protocol.GameStartPayload{
			Seed:    m.seed,
			Players: playerIDs,
		},
	})

	// Start the broadcast loop
	go m.broadcastLoop()
}

// broadcastLoop sends OpponentUpdate to all players every broadcastInterval.
func (m *Match) broadcastLoop() {
	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.RLock()
			phase := m.phase
			m.mu.RUnlock()

			if phase != PhasePlaying {
				return
			}
			m.sendOpponentUpdates()
		case <-m.stopCh:
			return
		}
	}
}

// sendOpponentUpdates builds and sends each player their opponents' states.
func (m *Match) sendOpponentUpdates() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Collect all snapshots
	allStates := make(map[string]protocol.OpponentState)
	for _, p := range m.players {
		p.mu.Lock()
		snap := p.Snapshot
		p.mu.Unlock()

		state := protocol.OpponentState{
			PlayerID:   p.ID,
			PlayerName: p.Name,
			Alive:      p.Alive,
		}
		if snap != nil {
			state.Score = snap.Score
			state.Level = snap.Level
			state.Lines = snap.Lines
			state.Board = snap.Board
			state.Alive = snap.Alive
		}
		allStates[p.ID] = state
	}

	// Send each player everyone else's state
	for _, p := range m.players {
		var opponents []protocol.OpponentState
		for id, state := range allStates {
			if id != p.ID {
				opponents = append(opponents, state)
			}
		}
		p.send(protocol.Envelope{
			Type:    protocol.MsgOpponentUpdate,
			Payload: protocol.OpponentUpdatePayload{Opponents: opponents},
		})
	}
}

func (m *Match) broadcastToAll(env protocol.Envelope) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.players {
		p.send(env)
	}
}

// handleLinesCleared calculates garbage and routes it to a random opponent.
func (m *Match) handleLinesCleared(attackerID string, payload protocol.LinesClearedPayload) {
	if payload.AttackPower <= 0 {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Pick a random alive opponent
	var targets []string
	for id, p := range m.players {
		if id != attackerID && p.Alive {
			targets = append(targets, id)
		}
	}

	if len(targets) == 0 {
		return
	}

	targetID := targets[rand.Intn(len(targets))]
	target := m.players[targetID]
	if target != nil {
		target.send(protocol.Envelope{
			Type: protocol.MsgReceiveGarbage,
			Payload: protocol.ReceiveGarbagePayload{
				Lines:      payload.AttackPower,
				AttackerID: attackerID,
			},
		})
	}
}

// handlePlayerDead marks a player as dead and checks for a winner.
func (m *Match) handlePlayerDead(playerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.players[playerID]; ok {
		p.Alive = false
	}

	m.checkWinCondition()
}

// checkWinCondition must be called with m.mu held.
func (m *Match) checkWinCondition() {
	var alive []*Player
	for _, p := range m.players {
		if p.Alive {
			alive = append(alive, p)
		}
	}

	if len(alive) <= 1 && len(m.players) >= minPlayers {
		m.phase = PhaseGameOver
		winnerID := ""
		winnerName := ""
		if len(alive) == 1 {
			winnerID = alive[0].ID
			winnerName = alive[0].Name
			m.winnerID = winnerID
		}

		// Compute ranks: alive player gets rank 1, dead players count from bottom
		totalPlayers := len(m.players)
		for _, p := range m.players {
			rank := totalPlayers // last place by default
			if p.ID == winnerID {
				rank = 1
			}
			p.send(protocol.Envelope{
				Type: protocol.MsgMatchOver,
				Payload: protocol.MatchOverPayload{
					WinnerID:   winnerID,
					WinnerName: winnerName,
					YourRank:   rank,
				},
			})
		}

		// Reset for next round
		go func() {
			time.Sleep(2 * time.Second)
			m.mu.Lock()
			m.phase = PhaseLobby
			for _, p := range m.players {
				p.Alive = true
				p.Ready = false
			}
			m.mu.Unlock()
			m.broadcastLobbyUpdate()
		}()
	}
}

func (m *Match) resetToLobby() {
	m.mu.Lock()
	m.phase = PhaseLobby
	for _, p := range m.players {
		p.Ready = false
		p.Alive = true
	}
	m.mu.Unlock()
}

// --- Hub ---

type Hub struct {
	mu      sync.RWMutex
	matches map[string]*Match
	nextID  int
}

func newHub() *Hub {
	return &Hub{
		matches: make(map[string]*Match),
	}
}

func (h *Hub) getOrCreateMainMatch() *Match {
	h.mu.Lock()
	defer h.mu.Unlock()

	matchID := "main"
	if m, ok := h.matches[matchID]; ok {
		return m
	}
	m := newMatch(matchID)
	h.matches[matchID] = m
	return m
}

func (h *Hub) generatePlayerID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	return fmt.Sprintf("player_%d_%d", time.Now().UnixMilli(), h.nextID)
}

// --- Connection Handler ---

func handleConnection(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}

	playerID := hub.generatePlayerID()
	p := newPlayer(playerID, conn)
	match := hub.getOrCreateMainMatch()

	// Send player their ID
	p.send(protocol.Envelope{
		Type:    protocol.MsgAssignID,
		Payload: protocol.AssignIDPayload{PlayerID: playerID},
	})

	// Start write pump
	go p.writePump()

	// Read pump (blocking)
	readPump(p, match)

	// Cleanup on disconnect
	match.removePlayer(playerID)
	log.Printf("Player %s (%s) disconnected", p.Name, playerID)

	if match.playerCount() == 0 {
		match.resetToLobby()
	} else {
		match.broadcastLobbyUpdate()
	}
}

// readPump reads messages from the WebSocket and dispatches them.
func readPump(p *Player, match *Match) {
	defer p.Conn.Close()

	p.Conn.SetReadLimit(maxMessageSize)
	p.Conn.SetReadDeadline(time.Now().Add(pongWait))
	p.Conn.SetPongHandler(func(string) error {
		p.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := p.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("read error for %s: %v", p.ID, err)
			}
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(message, &env); err != nil {
			log.Printf("unmarshal error from %s: %v", p.ID, err)
			continue
		}

		handleMessage(p, match, env, message)
	}
}

// handleMessage dispatches a client message.
func handleMessage(p *Player, match *Match, env protocol.Envelope, raw []byte) {
	switch env.Type {
	case protocol.MsgJoin:
		var payload protocol.JoinPayload
		if extractPayload(raw, &payload) == nil {
			p.Name = payload.PlayerName
			match.addPlayer(p)
			log.Printf("Player %s (%s) joined", p.Name, p.ID)
			match.broadcastLobbyUpdate()
		}

	case protocol.MsgReady:
		var payload protocol.ReadyPayload
		if extractPayload(raw, &payload) == nil {
			p.Ready = payload.Ready
			match.broadcastLobbyUpdate()

			if match.canStart() {
				match.startCountdown()
			}
		}

	case protocol.MsgBoardSnapshot:
		var payload protocol.BoardSnapshotPayload
		if extractPayload(raw, &payload) == nil {
			p.mu.Lock()
			p.Snapshot = &payload
			p.mu.Unlock()
		}

	case protocol.MsgLinesCleared:
		var payload protocol.LinesClearedPayload
		if extractPayload(raw, &payload) == nil {
			match.handleLinesCleared(p.ID, payload)
		}

	case protocol.MsgPlayerDead:
		match.handlePlayerDead(p.ID)

	default:
		log.Printf("unknown message type from %s: %s", p.ID, env.Type)
	}
}

// extractPayload re-unmarshals the raw JSON to extract a typed payload.
func extractPayload(raw []byte, target interface{}) error {
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return err
	}
	return json.Unmarshal(wrapper.Payload, target)
}

// --- Main ---

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	hub := newHub()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleConnection(hub, w, r)
	})

	// Simple health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("Gotris server starting on :%s", port)
	log.Printf("WebSocket endpoint: ws://localhost:%s/ws", port)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-done
	log.Println("Server shutting down...")
}

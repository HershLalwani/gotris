package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sort"
	"strings"
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
	roomCodeLength    = 5
)

// --- Upgrader ---

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// --- Player (server-side) ---

type Player struct {
	ID       string
	Name     string
	Ready    bool
	Alive    bool
	Conn     *websocket.Conn
	sendCh   chan []byte
	roomID   string
	TargetID string // who this player wants to attack ("" = random)
	// Latest snapshot from this client
	mu       sync.Mutex
	Snapshot *protocol.BoardSnapshotPayload
}

func newPlayer(id string, conn *websocket.Conn) *Player {
	return &Player{
		ID:     id,
		Conn:   conn,
		Alive:  true,
		sendCh: make(chan []byte, 64),
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
	// Recover from panic if sendCh was closed (player disconnected).
	defer func() { recover() }()
	select {
	case p.sendCh <- data:
	default:
		log.Printf("send channel full for player %s, dropping message", p.ID)
	}
}

// --- Room ---

type RoomPhase int

const (
	PhaseLobby RoomPhase = iota
	PhaseCountdown
	PhasePlaying
	PhaseGameOver
)

type Room struct {
	mu        sync.RWMutex
	code      string
	phase     RoomPhase
	players   map[string]*Player
	seed      int64
	countdown int
	winnerID  string
	stopCh    chan struct{}
}

func newRoom(code string) *Room {
	return &Room{
		code:    code,
		phase:   PhaseLobby,
		players: make(map[string]*Player),
		stopCh:  make(chan struct{}),
	}
}

func (r *Room) addPlayer(p *Player) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.players[p.ID] = p
	p.roomID = r.code
}

func (r *Room) removePlayer(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p, ok := r.players[id]; ok {
		p.roomID = ""
		delete(r.players, id)
	}

	// If we're playing and a player leaves, mark them dead
	if r.phase == PhasePlaying {
		r.checkWinCondition()
	}
}

func (r *Room) playerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.players)
}

func (r *Room) broadcastLobbyUpdate() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var players []protocol.LobbyPlayer
	for _, p := range r.players {
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

	for _, p := range r.players {
		p.send(env)
	}
}

func (r *Room) canStart() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.players) < minPlayers {
		return false
	}
	for _, p := range r.players {
		if !p.Ready {
			return false
		}
	}
	return true
}

func (r *Room) startCountdown() {
	r.mu.Lock()
	r.phase = PhaseCountdown
	r.countdown = 3
	r.mu.Unlock()

	go func() {
		for i := 3; i > 0; i-- {
			r.mu.Lock()
			r.countdown = i
			r.mu.Unlock()

			r.broadcastToAll(protocol.Envelope{
				Type:    protocol.MsgCountdown,
				Payload: protocol.CountdownPayload{Value: i},
			})
			time.Sleep(time.Second)
		}
		r.startGame()
	}()
}

func (r *Room) startGame() {
	r.mu.Lock()
	r.phase = PhasePlaying
	r.seed = rand.Int63()
	r.winnerID = ""

	var playerIDs []string
	for id, p := range r.players {
		playerIDs = append(playerIDs, id)
		p.Alive = true
		p.Ready = false
		p.mu.Lock()
		p.Snapshot = nil
		p.mu.Unlock()
	}
	r.mu.Unlock()

	r.broadcastToAll(protocol.Envelope{
		Type: protocol.MsgGameStart,
		Payload: protocol.GameStartPayload{
			Seed:    r.seed,
			Players: playerIDs,
		},
	})

	// Start the broadcast loop
	go r.broadcastLoop()
}

// broadcastLoop sends OpponentUpdate to all players every broadcastInterval.
func (r *Room) broadcastLoop() {
	ticker := time.NewTicker(broadcastInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.mu.RLock()
			phase := r.phase
			r.mu.RUnlock()

			if phase != PhasePlaying {
				return
			}
			r.sendOpponentUpdates()
		case <-r.stopCh:
			return
		}
	}
}

// sendOpponentUpdates builds and sends each player their opponents' states.
func (r *Room) sendOpponentUpdates() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect all snapshots
	allStates := make(map[string]protocol.OpponentState)
	for _, p := range r.players {
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

	// Send each player everyone else's state (sorted by ID for stable order)
	for _, p := range r.players {
		var opponents []protocol.OpponentState
		for id, state := range allStates {
			if id != p.ID {
				opponents = append(opponents, state)
			}
		}
		sort.Slice(opponents, func(i, j int) bool {
			return opponents[i].PlayerID < opponents[j].PlayerID
		})
		p.send(protocol.Envelope{
			Type:    protocol.MsgOpponentUpdate,
			Payload: protocol.OpponentUpdatePayload{Opponents: opponents},
		})
	}
}

func (r *Room) broadcastToAll(env protocol.Envelope) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.players {
		p.send(env)
	}
}

// handleLinesCleared calculates garbage and routes it to a random opponent.
func (r *Room) handleLinesCleared(attackerID string, payload protocol.LinesClearedPayload) {
	if payload.AttackPower <= 0 {
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	attacker := r.players[attackerID]
	if attacker == nil {
		return
	}

	// Determine target: use player's stored target if they're alive, else random.
	targetID := attacker.TargetID
	if targetID != "" {
		if t, ok := r.players[targetID]; !ok || !t.Alive || targetID == attackerID {
			targetID = "" // target invalid, fall back to random
		}
	}

	if targetID == "" {
		// Pick a random alive opponent
		var candidates []string
		for id, p := range r.players {
			if id != attackerID && p.Alive {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return
		}
		targetID = candidates[rand.Intn(len(candidates))]
	}

	target := r.players[targetID]
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
func (r *Room) handlePlayerDead(playerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p, ok := r.players[playerID]; ok {
		p.Alive = false
	}

	r.checkWinCondition()
}

// checkWinCondition must be called with r.mu held.
func (r *Room) checkWinCondition() {
	var alive []*Player
	for _, p := range r.players {
		if p.Alive {
			alive = append(alive, p)
		}
	}

	if len(alive) <= 1 && len(r.players) >= minPlayers {
		r.phase = PhaseGameOver
		winnerID := ""
		winnerName := ""
		if len(alive) == 1 {
			winnerID = alive[0].ID
			winnerName = alive[0].Name
			r.winnerID = winnerID
		}

		// Compute ranks: alive player gets rank 1, dead players last
		totalPlayers := len(r.players)
		for _, p := range r.players {
			rank := totalPlayers
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
			r.mu.Lock()
			r.phase = PhaseLobby
			for _, p := range r.players {
				p.Alive = true
				p.Ready = false
			}
			r.mu.Unlock()
			r.broadcastLobbyUpdate()
		}()
	}
}

func (r *Room) resetToLobby() {
	r.mu.Lock()
	r.phase = PhaseLobby
	for _, p := range r.players {
		p.Ready = false
		p.Alive = true
	}
	r.mu.Unlock()
}

// --- Hub ---

// PendingJoin tracks a player who created/joined a room via HTTP
// and is expected to connect via WebSocket with the given token.
type PendingJoin struct {
	RoomCode   string
	PlayerName string
	PlayerID   string
	CreatedAt  time.Time
}

type Hub struct {
	mu           sync.RWMutex
	rooms        map[string]*Room        // code -> Room
	players      map[string]*Player      // playerID -> Player
	pendingJoins map[string]*PendingJoin // token -> PendingJoin
	nextID       int
}

func newHub() *Hub {
	return &Hub{
		rooms:        make(map[string]*Room),
		players:      make(map[string]*Player),
		pendingJoins: make(map[string]*PendingJoin),
	}
}

func (h *Hub) generatePlayerID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	return fmt.Sprintf("player_%d_%d", time.Now().UnixMilli(), h.nextID)
}

func (h *Hub) generateRoomCode() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for {
		code := make([]byte, roomCodeLength)
		for i := range code {
			code[i] = charset[rand.Intn(len(charset))]
		}
		c := string(code)
		if _, exists := h.rooms[c]; !exists {
			return c
		}
	}
}

func (h *Hub) createRoom() *Room {
	h.mu.Lock()
	defer h.mu.Unlock()

	code := h.generateRoomCode()
	room := newRoom(code)
	h.rooms[code] = room
	log.Printf("Room %s created", code)
	return room
}

func (h *Hub) getRoom(code string) *Room {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rooms[strings.ToUpper(code)]
}

func (h *Hub) removeRoomIfEmpty(code string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room, ok := h.rooms[code]; ok {
		if room.playerCount() == 0 {
			// Signal broadcastLoop to stop (safety net).
			select {
			case <-room.stopCh:
			default:
				close(room.stopCh)
			}
			delete(h.rooms, code)
			log.Printf("Room %s removed (empty)", code)
			// Return freed memory to the OS in the background.
			go debug.FreeOSMemory()
		}
	}
}

func (h *Hub) generateToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	return fmt.Sprintf("tok_%d_%d", time.Now().UnixNano(), h.nextID)
}

func (h *Hub) addPendingJoin(token string, pj *PendingJoin) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Clean up expired tokens while we're here
	now := time.Now()
	for t, p := range h.pendingJoins {
		if now.Sub(p.CreatedAt) > 60*time.Second {
			delete(h.pendingJoins, t)
		}
	}
	h.pendingJoins[token] = pj
}

func (h *Hub) consumeToken(token string) *PendingJoin {
	h.mu.Lock()
	defer h.mu.Unlock()
	pj, ok := h.pendingJoins[token]
	if !ok {
		return nil
	}
	delete(h.pendingJoins, token)
	if time.Since(pj.CreatedAt) > 60*time.Second {
		return nil
	}
	return pj
}

func (h *Hub) addPlayer(p *Player) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.players[p.ID] = p
}

func (h *Hub) removePlayer(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.players, id)
}

// --- HTTP Handlers (Front Desk) ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func handleCreateRoom(hub *Hub, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.ErrorResponse{Error: "invalid request body"})
		return
	}

	if strings.TrimSpace(req.PlayerName) == "" {
		req.PlayerName = "Player"
	}

	room := hub.createRoom()
	playerID := hub.generatePlayerID()
	token := hub.generateToken()

	hub.addPendingJoin(token, &PendingJoin{
		RoomCode:   room.code,
		PlayerName: req.PlayerName,
		PlayerID:   playerID,
		CreatedAt:  time.Now(),
	})

	log.Printf("Room %s created via HTTP for player %q (pending token)", room.code, req.PlayerName)

	writeJSON(w, http.StatusOK, protocol.CreateRoomResponse{
		RoomID:    room.code,
		JoinToken: token,
	})
}

func handleJoinRoom(hub *Hub, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req protocol.JoinRoomHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.ErrorResponse{Error: "invalid request body"})
		return
	}

	code := strings.ToUpper(strings.TrimSpace(req.RoomID))
	room := hub.getRoom(code)
	if room == nil {
		writeJSON(w, http.StatusNotFound, protocol.ErrorResponse{Error: fmt.Sprintf("room %q not found", code)})
		return
	}

	room.mu.RLock()
	phase := room.phase
	room.mu.RUnlock()
	if phase != PhaseLobby {
		writeJSON(w, http.StatusConflict, protocol.ErrorResponse{Error: "game already in progress"})
		return
	}

	if strings.TrimSpace(req.PlayerName) == "" {
		req.PlayerName = "Player"
	}

	playerID := hub.generatePlayerID()
	token := hub.generateToken()

	hub.addPendingJoin(token, &PendingJoin{
		RoomCode:   code,
		PlayerName: req.PlayerName,
		PlayerID:   playerID,
		CreatedAt:  time.Now(),
	})

	log.Printf("Player %q joining room %s via HTTP (pending token)", req.PlayerName, code)

	writeJSON(w, http.StatusOK, protocol.JoinRoomHTTPResponse{
		RoomID:    code,
		JoinToken: token,
	})
}

func handleListRooms(hub *Hub, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hub.mu.RLock()
	rooms := make([]protocol.RoomInfo, 0, len(hub.rooms))
	for _, room := range hub.rooms {
		room.mu.RLock()
		phaseStr := "lobby"
		switch room.phase {
		case PhaseCountdown:
			phaseStr = "countdown"
		case PhasePlaying:
			phaseStr = "playing"
		case PhaseGameOver:
			phaseStr = "game_over"
		}
		rooms = append(rooms, protocol.RoomInfo{
			RoomID:      room.code,
			PlayerCount: len(room.players),
			MaxPlayers:  8,
			Phase:       phaseStr,
		})
		room.mu.RUnlock()
	}
	hub.mu.RUnlock()

	writeJSON(w, http.StatusOK, protocol.ListRoomsResponse{Rooms: rooms})
}

// --- WebSocket Handler (Game Room) ---

// handlePlay upgrades to WebSocket for a player who already has a join token.
func handlePlay(hub *Hub, w http.ResponseWriter, r *http.Request) {
	roomCode := r.URL.Query().Get("room")
	token := r.URL.Query().Get("token")

	if roomCode == "" || token == "" {
		http.Error(w, "missing room or token query parameter", http.StatusBadRequest)
		return
	}

	// Validate and consume token
	pj := hub.consumeToken(token)
	if pj == nil {
		http.Error(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	if pj.RoomCode != strings.ToUpper(roomCode) {
		http.Error(w, "token does not match room", http.StatusForbidden)
		return
	}

	room := hub.getRoom(pj.RoomCode)
	if room == nil {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}

	// Create the player from pending join info
	p := newPlayer(pj.PlayerID, conn)
	p.Name = pj.PlayerName
	p.Ready = false
	p.Alive = true

	hub.addPlayer(p)
	room.addPlayer(p)

	log.Printf("Player %s (%s) connected to room %s via WebSocket", p.Name, p.ID, room.code)

	// Send player their ID
	p.send(protocol.Envelope{
		Type:    protocol.MsgAssignID,
		Payload: protocol.AssignIDPayload{PlayerID: p.ID},
	})

	// Start write pump
	go p.writePump()

	// Broadcast lobby update so everyone sees the new player
	room.broadcastLobbyUpdate()

	// Read pump (blocking)
	readPump(p, hub)

	// Cleanup on disconnect
	room.removePlayer(p.ID)
	close(p.sendCh) // immediately stops writePump goroutine
	p.mu.Lock()
	p.Snapshot = nil // free board data
	p.mu.Unlock()
	log.Printf("Player %s (%s) left room %s", p.Name, p.ID, room.code)
	if room.playerCount() == 0 {
		room.resetToLobby()
		hub.removeRoomIfEmpty(room.code)
	} else {
		room.broadcastLobbyUpdate()
	}
	hub.removePlayer(p.ID)
	log.Printf("Player %s (%s) disconnected", p.Name, p.ID)
}

// readPump reads messages from the WebSocket and dispatches them.
func readPump(p *Player, hub *Hub) {
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

		handleMessage(p, hub, env, message)
	}
}

// handleMessage dispatches a client message.
func handleMessage(p *Player, hub *Hub, env protocol.Envelope, raw []byte) {
	switch env.Type {
	case protocol.MsgLeaveRoom:
		if p.roomID != "" {
			code := p.roomID
			room := hub.getRoom(code)
			if room != nil {
				room.removePlayer(p.ID)
				log.Printf("Player %s (%s) left room %s via message", p.Name, p.ID, code)
				if room.playerCount() == 0 {
					room.resetToLobby()
					hub.removeRoomIfEmpty(code)
				} else {
					room.broadcastLobbyUpdate()
				}
			}
		}

	case protocol.MsgReady:
		var payload protocol.ReadyPayload
		if extractPayload(raw, &payload) == nil {
			room := hub.getRoom(p.roomID)
			if room == nil {
				return
			}
			p.Ready = payload.Ready
			room.broadcastLobbyUpdate()

			if room.canStart() {
				room.startCountdown()
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
			room := hub.getRoom(p.roomID)
			if room != nil {
				room.handleLinesCleared(p.ID, payload)
			}
		}

	case protocol.MsgSetTarget:
		var payload protocol.SetTargetPayload
		if extractPayload(raw, &payload) == nil {
			p.mu.Lock()
			p.TargetID = payload.TargetID
			p.mu.Unlock()
		}

	case protocol.MsgPlayerDead:
		room := hub.getRoom(p.roomID)
		if room != nil {
			room.handlePlayerDead(p.ID)
		}

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

	// --- HTTP endpoints (Front Desk) ---
	http.HandleFunc("/create-room", func(w http.ResponseWriter, r *http.Request) {
		handleCreateRoom(hub, w, r)
	})
	http.HandleFunc("/join-room", func(w http.ResponseWriter, r *http.Request) {
		handleJoinRoom(hub, w, r)
	})
	http.HandleFunc("/list-rooms", func(w http.ResponseWriter, r *http.Request) {
		handleListRooms(hub, w, r)
	})

	// --- WebSocket endpoint (Game Room) ---
	http.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		handlePlay(hub, w, r)
	})

	// Simple health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("Gotris server starting on :%s", port)
	log.Printf("HTTP endpoints: http://localhost:%s/create-room, /join-room, /list-rooms", port)
	log.Printf("WebSocket endpoint: ws://localhost:%s/play?room=XXXXX&token=...", port)

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

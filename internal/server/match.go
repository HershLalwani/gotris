package server

import (
	"sync"
	"time"

	"github.com/hersh/gotris/internal/game"
	"github.com/hersh/gotris/internal/player"
)

type GamePhase int

const (
	PhaseLobby GamePhase = iota
	PhaseCountdown
	PhasePlaying
	PhaseGameOver
)

type Match struct {
	mu           sync.RWMutex
	lobby        *player.Lobby
	gameStates   map[string]*game.GameState
	phase        GamePhase
	countdown    int
	winnerID     string
	tickers      map[string]*time.Ticker
	attackChan   chan AttackMessage
	gameOverChan chan string
}

type AttackMessage struct {
	AttackerID string
	TargetID   string
	Lines      int
}

func NewMatch() *Match {
	return &Match{
		lobby:        player.NewLobby(),
		gameStates:   make(map[string]*game.GameState),
		phase:        PhaseLobby,
		tickers:      make(map[string]*time.Ticker),
		attackChan:   make(chan AttackMessage, 100),
		gameOverChan: make(chan string, 100),
	}
}

func (m *Match) AddPlayer(id, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lobby.AddPlayer(id, name)
	m.gameStates[id] = game.NewGameState(id, name)
}

func (m *Match) RemovePlayer(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lobby.RemovePlayer(id)
	delete(m.gameStates, id)

	if t, ok := m.tickers[id]; ok {
		t.Stop()
		delete(m.tickers, id)
	}
}

func (m *Match) GetGameState(id string) *game.GameState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gameStates[id]
}

func (m *Match) GetAllGameStates() map[string]*game.GameState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make(map[string]*game.GameState)
	for k, v := range m.gameStates {
		states[k] = v
	}
	return states
}

func (m *Match) SetPlayerReady(id string, ready bool) {
	m.lobby.SetPlayerReady(id, ready)
}

func (m *Match) GetPhase() GamePhase {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.phase
}

func (m *Match) GetLobby() *player.Lobby {
	return m.lobby
}

func (m *Match) CanStart() bool {
	return m.lobby.Count() >= 2 && m.lobby.Count() == m.lobby.CountReady()
}

func (m *Match) StartCountdown() {
	m.mu.Lock()
	m.phase = PhaseCountdown
	m.countdown = 3
	m.mu.Unlock()
}

func (m *Match) DecrementCountdown() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.countdown--
	return m.countdown
}

func (m *Match) GetCountdown() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.countdown
}

func (m *Match) StartGame() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.phase = PhasePlaying
	m.lobby.Reset()

	for id, gs := range m.gameStates {
		*gs = *game.NewGameState(id, gs.PlayerName)
	}

	for _, p := range m.lobby.GetAllPlayers() {
		p.AttackTarget = m.lobby.GetRandomAliveTarget(p.ID)
	}
}

func (m *Match) MoveLeft(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		return gs.MoveLeft()
	}
	return false
}

func (m *Match) MoveRight(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		return gs.MoveRight()
	}
	return false
}

func (m *Match) MoveDown(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		return gs.MoveDown()
	}
	return false
}

func (m *Match) HardDrop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		gs.HardDrop()
		m.processAttack(id)
	}
}

func (m *Match) Rotate(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		return gs.Rotate()
	}
	return false
}

func (m *Match) Hold(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		return gs.Hold()
	}
	return false
}

func (m *Match) Tick(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if gs, ok := m.gameStates[id]; ok && !gs.IsGameOver {
		gs.Tick()
		if gs.AttackPower > 0 {
			m.processAttack(id)
		}
		if gs.IsGameOver {
			m.handleGameOver(id)
		}
	}
}

func (m *Match) processAttack(attackerID string) {
	attacker := m.lobby.GetPlayer(attackerID)
	if attacker == nil {
		return
	}

	gs := m.gameStates[attackerID]
	if gs == nil || gs.AttackPower == 0 {
		return
	}

	targetID := attacker.AttackTarget
	if targetID == "" {
		targetID = m.lobby.GetRandomAliveTarget(attackerID)
	}

	if targetID != "" {
		select {
		case m.attackChan <- AttackMessage{
			AttackerID: attackerID,
			TargetID:   targetID,
			Lines:      gs.AttackPower,
		}:
		default:
		}
	}

	gs.AttackPower = 0
}

func (m *Match) handleGameOver(id string) {
	m.lobby.SetPlayerAlive(id, false)

	for _, p := range m.lobby.GetAllPlayers() {
		if p.AttackTarget == id {
			p.AttackTarget = m.lobby.GetRandomAliveTarget(p.ID)
		}
	}

	select {
	case m.gameOverChan <- id:
	default:
	}

	aliveCount := m.lobby.CountAlive()
	if aliveCount <= 1 {
		m.phase = PhaseGameOver
		alive := m.lobby.GetAlivePlayers()
		if len(alive) == 1 {
			m.winnerID = alive[0].ID
			m.gameStates[m.winnerID].IsWinner = true
		}
	}
}

func (m *Match) GetAttackChan() <-chan AttackMessage {
	return m.attackChan
}

func (m *Match) GetGameOverChan() <-chan string {
	return m.gameOverChan
}

func (m *Match) ApplyAttack(targetID string, lines int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if gs, ok := m.gameStates[targetID]; ok {
		gs.ReceiveGarbage(lines)
	}
}

func (m *Match) GetWinnerID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.winnerID
}

func (m *Match) SetAttackTarget(playerID, targetID string) {
	m.lobby.SetAttackTarget(playerID, targetID)
}

func (m *Match) GetRandomTarget(excludeID string) string {
	return m.lobby.GetRandomAliveTarget(excludeID)
}

func (m *Match) IsPlayerAlive(id string) bool {
	p := m.lobby.GetPlayer(id)
	return p != nil && p.IsAlive
}

func (m *Match) GetDropSpeed(id string) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if gs, ok := m.gameStates[id]; ok {
		return gs.GetDropSpeed()
	}
	return 800 * time.Millisecond
}

func (m *Match) SetRandomTargets() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.lobby.GetAllPlayers() {
		if p.IsAlive {
			p.AttackTarget = m.lobby.GetRandomAliveTarget(p.ID)
		}
	}
}

type GameManager struct {
	mu        sync.RWMutex
	matches   map[string]*Match
	playerMap map[string]string
}

func NewGameManager() *GameManager {
	return &GameManager{
		matches:   make(map[string]*Match),
		playerMap: make(map[string]string),
	}
}

func (gm *GameManager) CreateMatch(matchID string) *Match {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	match := NewMatch()
	gm.matches[matchID] = match
	return match
}

func (gm *GameManager) GetMatch(matchID string) *Match {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.matches[matchID]
}

func (gm *GameManager) GetPlayerMatch(playerID string) *Match {
	gm.mu.RLock()
	matchID := gm.playerMap[playerID]
	gm.mu.RUnlock()
	return gm.GetMatch(matchID)
}

func (gm *GameManager) JoinMatch(matchID, playerID, playerName string) *Match {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	match, ok := gm.matches[matchID]
	if !ok {
		match = NewMatch()
		gm.matches[matchID] = match
	}

	match.AddPlayer(playerID, playerName)
	gm.playerMap[playerID] = matchID

	return match
}

func (gm *GameManager) LeaveMatch(playerID string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	matchID := gm.playerMap[playerID]
	if match, ok := gm.matches[matchID]; ok {
		match.RemovePlayer(playerID)
		if match.lobby.Count() == 0 {
			delete(gm.matches, matchID)
		}
	}
	delete(gm.playerMap, playerID)
}

func (gm *GameManager) GetOrCreateMainMatch() *Match {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	matchID := "main"
	if match, ok := gm.matches[matchID]; ok {
		return match
	}

	match := NewMatch()
	gm.matches[matchID] = match
	return match
}

func (gm *GameManager) BroadcastAttack() {
	for _, match := range gm.matches {
		go func(m *Match) {
			for attack := range m.GetAttackChan() {
				targetID := attack.TargetID
				if !m.IsPlayerAlive(targetID) {
					targetID = m.GetRandomTarget(attack.AttackerID)
				}
				if targetID != "" {
					m.ApplyAttack(targetID, attack.Lines)
				}
			}
		}(match)
	}
}

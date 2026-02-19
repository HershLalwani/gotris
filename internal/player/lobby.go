package player

import (
	"sync"
)

type Player struct {
	ID           string
	Name         string
	Ready        bool
	IsAlive      bool
	AttackTarget string
	Kills        int
}

type Lobby struct {
	mu      sync.RWMutex
	players map[string]*Player
}

func NewLobby() *Lobby {
	return &Lobby{
		players: make(map[string]*Player),
	}
}

func (l *Lobby) AddPlayer(id, name string) *Player {
	l.mu.Lock()
	defer l.mu.Unlock()

	player := &Player{
		ID:      id,
		Name:    name,
		Ready:   false,
		IsAlive: true,
		Kills:   0,
	}
	l.players[id] = player
	return player
}

func (l *Lobby) RemovePlayer(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.players, id)
}

func (l *Lobby) GetPlayer(id string) *Player {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.players[id]
}

func (l *Lobby) SetPlayerReady(id string, ready bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p, ok := l.players[id]; ok {
		p.Ready = ready
	}
}

func (l *Lobby) SetPlayerAlive(id string, alive bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p, ok := l.players[id]; ok {
		p.IsAlive = alive
	}
}

func (l *Lobby) SetAttackTarget(id string, targetID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p, ok := l.players[id]; ok {
		p.AttackTarget = targetID
	}
}

func (l *Lobby) GetAllPlayers() []*Player {
	l.mu.RLock()
	defer l.mu.RUnlock()

	players := make([]*Player, 0, len(l.players))
	for _, p := range l.players {
		players = append(players, p)
	}
	return players
}

func (l *Lobby) GetAlivePlayers() []*Player {
	l.mu.RLock()
	defer l.mu.RUnlock()

	players := make([]*Player, 0)
	for _, p := range l.players {
		if p.IsAlive {
			players = append(players, p)
		}
	}
	return players
}

func (l *Lobby) GetReadyPlayers() []*Player {
	l.mu.RLock()
	defer l.mu.RUnlock()

	players := make([]*Player, 0)
	for _, p := range l.players {
		if p.Ready {
			players = append(players, p)
		}
	}
	return players
}

func (l *Lobby) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.players)
}

func (l *Lobby) CountAlive() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	count := 0
	for _, p := range l.players {
		if p.IsAlive {
			count++
		}
	}
	return count
}

func (l *Lobby) CountReady() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	count := 0
	for _, p := range l.players {
		if p.Ready {
			count++
		}
	}
	return count
}

func (l *Lobby) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.players {
		p.IsAlive = true
		p.Ready = false
		p.AttackTarget = ""
		p.Kills = 0
	}
}

func (l *Lobby) IncrementKills(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p, ok := l.players[id]; ok {
		p.Kills++
	}
}

func (l *Lobby) GetRandomAliveTarget(excludeID string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	alive := make([]string, 0)
	for _, p := range l.players {
		if p.IsAlive && p.ID != excludeID {
			alive = append(alive, p.ID)
		}
	}

	if len(alive) == 0 {
		return ""
	}

	return alive[len(alive)%len(alive)]
}

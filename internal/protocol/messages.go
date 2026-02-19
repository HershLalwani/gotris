package protocol

// MessageType identifies the kind of message sent over the wire.
type MessageType string

const (
	// Server -> Client messages
	MsgAssignID       MessageType = "assign_id"
	MsgGameStart      MessageType = "game_start"
	MsgCountdown      MessageType = "countdown"
	MsgOpponentUpdate MessageType = "opponent_update"
	MsgReceiveGarbage MessageType = "receive_garbage"
	MsgGameOver       MessageType = "game_over"
	MsgLobbyUpdate    MessageType = "lobby_update"
	MsgMatchOver      MessageType = "match_over"

	// Client -> Server messages
	MsgJoin          MessageType = "join"
	MsgReady         MessageType = "ready"
	MsgBoardSnapshot MessageType = "board_snapshot"
	MsgLinesCleared  MessageType = "lines_cleared"
	MsgPlayerDead    MessageType = "player_dead"
)

// Envelope is the top-level wire format for all messages.
type Envelope struct {
	Type    MessageType `json:"type"`
	Payload interface{} `json:"payload"`
}

// --- Server -> Client payloads ---

// AssignIDPayload is sent when a client first connects.
type AssignIDPayload struct {
	PlayerID string `json:"player_id"`
}

// GameStartPayload tells all clients to begin the game.
type GameStartPayload struct {
	Seed    int64    `json:"seed"`
	Players []string `json:"players"` // list of player IDs in the match
}

// CountdownPayload carries the countdown tick value.
type CountdownPayload struct {
	Value int `json:"value"`
}

// OpponentState is a compressed snapshot of one opponent's board.
type OpponentState struct {
	PlayerID   string `json:"player_id"`
	PlayerName string `json:"player_name"`
	Score      int    `json:"score"`
	Level      int    `json:"level"`
	Lines      int    `json:"lines"`
	Alive      bool   `json:"alive"`
	IsWinner   bool   `json:"is_winner"`
	// Board is a flat array: BoardHeight * BoardWidth cells.
	// Each value is a color index (0 = empty).
	Board []int `json:"board"`
}

// OpponentUpdatePayload carries snapshots of all opponents.
type OpponentUpdatePayload struct {
	Opponents []OpponentState `json:"opponents"`
}

// ReceiveGarbagePayload tells a client to buffer incoming garbage.
type ReceiveGarbagePayload struct {
	Lines      int    `json:"lines"`
	AttackerID string `json:"attacker_id"`
}

// GameOverPayload informs a client that the match ended.
type GameOverPayload struct {
	WinnerID   string `json:"winner_id"`
	WinnerName string `json:"winner_name"`
}

// LobbyPlayer is one player entry in a lobby update.
type LobbyPlayer struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
}

// LobbyUpdatePayload is sent whenever the lobby state changes.
type LobbyUpdatePayload struct {
	Players []LobbyPlayer `json:"players"`
}

// MatchOverPayload is sent when the match concludes (last player standing).
type MatchOverPayload struct {
	WinnerID   string `json:"winner_id"`
	WinnerName string `json:"winner_name"`
	YourRank   int    `json:"your_rank"`
}

// --- Client -> Server payloads ---

// JoinPayload is sent when a client wants to join the match.
type JoinPayload struct {
	PlayerName string `json:"player_name"`
}

// ReadyPayload toggles ready status.
type ReadyPayload struct {
	Ready bool `json:"ready"`
}

// BoardSnapshotPayload is the client's current board state.
type BoardSnapshotPayload struct {
	Score int   `json:"score"`
	Level int   `json:"level"`
	Lines int   `json:"lines"`
	Alive bool  `json:"alive"`
	Board []int `json:"board"` // flat array, BoardHeight * BoardWidth
}

// LinesClearedPayload informs the server that lines were cleared.
type LinesClearedPayload struct {
	Count       int `json:"count"`
	AttackPower int `json:"attack_power"`
}

// PlayerDeadPayload informs the server this player has died.
type PlayerDeadPayload struct{}

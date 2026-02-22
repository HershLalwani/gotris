package netclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
	"github.com/hersh/gotris/internal/protocol"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingInterval   = (pongWait * 9) / 10
	maxMessageSize = 16384
)

// --- tea.Msg types ---

// ServerMsg wraps an incoming WebSocket server message.
type ServerMsg struct {
	Type protocol.MessageType
	Raw  json.RawMessage
}

// ConnectedMsg is sent when the WS connects and receives its PlayerID.
type ConnectedMsg struct {
	PlayerID string
}

// DisconnectedMsg is sent when the WebSocket connection drops unexpectedly.
type DisconnectedMsg struct {
	Err error
}

// RoomCreatedHTTPMsg is the result of an HTTP POST /create-room + WS connect.
type RoomCreatedHTTPMsg struct {
	RoomID string
	Token  string
	Err    error
}

// RoomJoinedHTTPMsg is the result of an HTTP POST /join-room + WS connect.
type RoomJoinedHTTPMsg struct {
	RoomID string
	Token  string
	Err    error
}

// RoomsListedMsg is the result of an HTTP GET /list-rooms.
type RoomsListedMsg struct {
	Rooms []protocol.RoomInfo
	Err   error
}

// --- Client ---

// Client manages HTTP and WebSocket connections to the game server.
// HTTP is used for room creation/listing (Front Desk).
// WebSocket is used for real-time gameplay (Game Room).
type Client struct {
	mu         sync.Mutex
	httpBase   string // e.g. "http://localhost:8080"
	wsBase     string // e.g. "ws://localhost:8080"
	httpClient *http.Client

	// WebSocket (created on demand when joining a room)
	conn     *websocket.Conn
	sendCh   chan []byte
	program  *tea.Program
	done     chan struct{}
	wsActive bool
}

// New creates a Client that talks to the given HTTP base URL.
// No connections are opened; the client starts immediately.
func New(httpBaseURL string) *Client {
	wsBase := strings.Replace(httpBaseURL, "https://", "wss://", 1)
	wsBase = strings.Replace(wsBase, "http://", "ws://", 1)

	return &Client{
		httpBase:   httpBaseURL,
		wsBase:     wsBase,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		sendCh:     make(chan []byte, 256),
	}
}

// SetProgram sets the bubbletea program so the client can send tea.Msgs to it.
func (c *Client) SetProgram(p *tea.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.program = p
}

// --- HTTP methods (Front Desk) ---

// CreateRoom calls POST /create-room and returns the room ID and join token.
func (c *Client) CreateRoom(playerName string) (roomID, token string, err error) {
	reqBody := protocol.CreateRoomRequest{PlayerName: playerName}
	data, _ := json.Marshal(reqBody)

	resp, err := c.httpClient.Post(c.httpBase+"/create-room", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("server unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var errResp protocol.ErrorResponse
		json.Unmarshal(body, &errResp)
		return "", "", fmt.Errorf("%s", errResp.Error)
	}

	var result protocol.CreateRoomResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}
	return result.RoomID, result.JoinToken, nil
}

// JoinRoom calls POST /join-room and returns the join token.
func (c *Client) JoinRoom(roomID, playerName string) (token string, err error) {
	reqBody := protocol.JoinRoomHTTPRequest{RoomID: roomID, PlayerName: playerName}
	data, _ := json.Marshal(reqBody)

	resp, err := c.httpClient.Post(c.httpBase+"/join-room", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("server unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var errResp protocol.ErrorResponse
		json.Unmarshal(body, &errResp)
		return "", fmt.Errorf("%s", errResp.Error)
	}

	var result protocol.JoinRoomHTTPResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	return result.JoinToken, nil
}

// ListRooms calls GET /list-rooms and returns the active rooms.
func (c *Client) ListRooms() ([]protocol.RoomInfo, error) {
	resp, err := c.httpClient.Get(c.httpBase + "/list-rooms")
	if err != nil {
		return nil, fmt.Errorf("server unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result protocol.ListRoomsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result.Rooms, nil
}

// --- WebSocket methods (Game Room) ---

// ConnectToRoom opens a WebSocket to /play?room=...&token=... and starts pumps.
func (c *Client) ConnectToRoom(roomID, token string) error {
	c.mu.Lock()
	if c.wsActive {
		c.mu.Unlock()
		c.DisconnectFromRoom()
		c.mu.Lock()
	}
	c.mu.Unlock()

	wsURL := fmt.Sprintf("%s/play?room=%s&token=%s", c.wsBase, roomID, token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket connection failed: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.sendCh = make(chan []byte, 256)
	c.done = make(chan struct{})
	c.wsActive = true
	c.mu.Unlock()

	go c.writePump()
	go c.readPump()

	return nil
}

// DisconnectFromRoom gracefully closes the WebSocket without destroying the client.
func (c *Client) DisconnectFromRoom() {
	c.mu.Lock()
	if !c.wsActive {
		c.mu.Unlock()
		return
	}
	c.wsActive = false

	// Signal goroutines to stop
	select {
	case <-c.done:
	default:
		close(c.done)
	}

	if c.conn != nil {
		c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()
}

// Send marshals and sends an envelope over the active WebSocket.
func (c *Client) Send(env protocol.Envelope) {
	c.mu.Lock()
	active := c.wsActive
	c.mu.Unlock()

	if !active {
		return
	}

	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("client marshal error: %v", err)
		return
	}
	select {
	case c.sendCh <- data:
	default:
		log.Printf("client send channel full, dropping message")
	}
}

// Close shuts down the client entirely.
func (c *Client) Close() {
	c.DisconnectFromRoom()
}

// IsWSActive returns whether a WebSocket connection is active.
func (c *Client) IsWSActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wsActive
}

// --- Pumps ---

// readPump reads messages from the WebSocket and sends them to the bubbletea program.
func (c *Client) readPump() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return
	}

	defer func() {
		c.mu.Lock()
		p := c.program
		active := c.wsActive // false = intentional disconnect, don't notify
		c.mu.Unlock()
		if p != nil && active {
			p.Send(DisconnectedMsg{})
		}
	}()

	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("readPump error: %v", err)
			}
			return
		}

		var env struct {
			Type    protocol.MessageType `json:"type"`
			Payload json.RawMessage      `json:"payload"`
		}
		if err := json.Unmarshal(message, &env); err != nil {
			log.Printf("client unmarshal error: %v", err)
			continue
		}

		c.mu.Lock()
		p := c.program
		c.mu.Unlock()

		if p == nil {
			continue
		}

		switch env.Type {
		case protocol.MsgAssignID:
			var payload protocol.AssignIDPayload
			if json.Unmarshal(env.Payload, &payload) == nil {
				p.Send(ConnectedMsg{PlayerID: payload.PlayerID})
			}
		default:
			p.Send(ServerMsg{Type: env.Type, Raw: env.Payload})
		}
	}
}

// writePump writes messages from sendCh to the WebSocket.
func (c *Client) writePump() {
	c.mu.Lock()
	sendCh := c.sendCh
	done := c.done
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return
	}

	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		conn.Close()
	}()

	for {
		select {
		case msg, ok := <-sendCh:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

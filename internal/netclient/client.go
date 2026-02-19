package netclient

import (
	"encoding/json"
	"log"
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

// ServerMsg is a tea.Msg that wraps an incoming server message.
type ServerMsg struct {
	Type protocol.MessageType
	Raw  json.RawMessage
}

// ConnectedMsg is sent when the client connects and receives its PlayerID.
type ConnectedMsg struct {
	PlayerID string
}

// DisconnectedMsg is sent when the WebSocket connection is lost.
type DisconnectedMsg struct {
	Err error
}

// Client manages the WebSocket connection to the game server.
type Client struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	sendCh  chan []byte
	program *tea.Program
	done    chan struct{}
	closed  bool
}

// New creates a Client connected to the given server URL.
func New(serverURL string) (*Client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return nil, err
	}

	c := &Client{
		conn:   conn,
		sendCh: make(chan []byte, 256),
		done:   make(chan struct{}),
	}

	return c, nil
}

// SetProgram sets the bubbletea program so the client can send messages to it.
func (c *Client) SetProgram(p *tea.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.program = p
}

// Start launches the read and write pumps.
func (c *Client) Start() {
	go c.writePump()
	go c.readPump()
}

// Send marshals and sends an envelope to the server.
func (c *Client) Send(env protocol.Envelope) {
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

// Close shuts down the client connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
	c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.conn.Close()
}

// readPump reads messages from the WebSocket and sends them to the bubbletea program.
func (c *Client) readPump() {
	defer func() {
		c.mu.Lock()
		p := c.program
		c.mu.Unlock()
		if p != nil {
			p.Send(DisconnectedMsg{})
		}
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("readPump error: %v", err)
			}
			return
		}

		// Parse the envelope to get the type
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

		// Dispatch special messages vs. generic ServerMsg
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
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.sendCh:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

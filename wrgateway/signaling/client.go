package signaling

import (
	"crypto/tls"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	TypeRegisterGateway = "register_gateway"
	TypeRegisterSuccess = "register_success"
	TypeRegisterError   = "register_error"
	TypeBrowserConnect  = "browser_connect"
	TypeConnectSuccess  = "connect_success"
	TypeConnectError    = "connect_error"
	TypeOffer           = "offer"
	TypeAnswer          = "answer"
	TypeCandidate       = "candidate"
	TypeBye             = "bye"
	TypePing            = "ping"
	TypePong            = "pong"
)

type Message struct {
	Type      string          `json:"type"`
	RoomID    string          `json:"room_id,omitempty"`
	AgentID   string          `json:"agent_id,omitempty"`
	GatewayID string          `json:"gateway_id,omitempty"`
	Token     string          `json:"token,omitempty"`
	Version   string          `json:"version,omitempty"`
	SDP       json.RawMessage `json:"sdp,omitempty"`
	Candidate json.RawMessage `json:"candidate,omitempty"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type Client struct {
	serverURL          string
	gatewayID          string
	gatewayToken       string
	conn               *websocket.Conn
	mu                 sync.Mutex
	sendCh             chan []byte
	done               chan struct{}
	onMessage          func(msg *Message)
	insecureSkipVerify bool
	stopCh             chan struct{}
	version            string
	iceServers         []json.RawMessage
}

func (c *Client) SetICEServers(sv []json.RawMessage) {
	c.mu.Lock()
	c.iceServers = sv
	c.mu.Unlock()
}

func (c *Client) ICEServers() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.iceServers
}

func NewClient(serverURL, gatewayID, gatewayToken string, insecureSkipVerify bool, version string) *Client {
	return &Client{
		serverURL:          serverURL,
		gatewayID:          gatewayID,
		gatewayToken:       gatewayToken,
		sendCh:             make(chan []byte, 64),
		done:               make(chan struct{}),
		stopCh:             make(chan struct{}),
		insecureSkipVerify: insecureSkipVerify,
		version:            version,
	}
}

func (c *Client) ConnectLoop() {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		err := c.connect()
		if err != nil {
			log.Printf("[SIG] connection failed: %v, retrying in %v", err, backoff)
			select {
			case <-c.stopCh:
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}

		backoff = 1 * time.Second

		<-c.done
		log.Printf("[SIG] connection lost, reconnecting...")
		select {
		case <-c.stopCh:
			return
		case <-time.After(backoff):
		}
	}
}

func (c *Client) connect() error {
	dialer := websocket.Dialer{
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: true,
		WriteBufferSize:   64 * 1024,
		ReadBufferSize:    64 * 1024,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: c.insecureSkipVerify,
		},
	}
	conn, _, err := dialer.Dial(c.serverURL, nil)
	if err != nil {
		return err
	}
	c.conn = conn

	c.done = make(chan struct{})
	c.sendCh = make(chan []byte, 64)

	reg := Message{
		Type:      TypeRegisterGateway,
		GatewayID: c.gatewayID,
		Token:     c.gatewayToken,
		Version:   c.version,
	}
	data, _ := json.Marshal(reg)
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		conn.Close()
		return err
	}

	_, respBytes, err := c.conn.ReadMessage()
	if err != nil {
		conn.Close()
		return err
	}
	var resp Message
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		conn.Close()
		return err
	}
	if resp.Type == TypeRegisterError {
		conn.Close()
		return &SignalingError{Detail: resp.Error}
	}

	// Parse ICE servers from register_success
	if resp.Type == TypeRegisterSuccess {
		var regResp struct {
			Type       string            `json:"type"`
			AgentID    string            `json:"agent_id,omitempty"`
			GatewayID  string            `json:"gateway_id,omitempty"`
			ICEServers []json.RawMessage `json:"ice_servers,omitempty"`
		}
		if err := json.Unmarshal(respBytes, &regResp); err == nil && len(regResp.ICEServers) > 0 {
			c.SetICEServers(regResp.ICEServers)
			log.Printf("[SIG] received %d ICE servers", len(c.ICEServers()))
		}
	}

	go c.readPump()
	go c.writePump()
	return nil
}

func (c *Client) readPump() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case TypePong:
			continue
		case TypePing:
			pong := Message{Type: TypePong}
			pongData, _ := json.Marshal(pong)
			c.mu.Lock()
			c.conn.WriteMessage(websocket.TextMessage, pongData)
			c.mu.Unlock()
		default:
			if c.onMessage != nil {
				c.onMessage(&msg)
			}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg := <-c.sendCh:
			c.mu.Lock()
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				c.mu.Unlock()
				log.Printf("[SIG] write failed: %v", err)
				_ = c.conn.Close()
				return
			}
			c.mu.Unlock()
		case <-ticker.C:
			// 服务端 last_seen 只认 type=heartbeat（旧版发 ping 会被看门狗当离线踢掉）
			c.mu.Lock()
			hb, _ := json.Marshal(Message{
				Type:      "heartbeat",
				AgentID:   c.gatewayID,
				GatewayID: c.gatewayID,
			})
			if err := c.conn.WriteMessage(websocket.TextMessage, hb); err != nil {
				c.mu.Unlock()
				log.Printf("[SIG] heartbeat write failed: %v", err)
				_ = c.conn.Close()
				return
			}
			// 兼容仍监听 ping 的服务端
			ping, _ := json.Marshal(Message{Type: TypePing, GatewayID: c.gatewayID})
			if err := c.conn.WriteMessage(websocket.TextMessage, ping); err != nil {
				c.mu.Unlock()
				log.Printf("[SIG] ping write failed: %v", err)
				_ = c.conn.Close()
				return
			}
			c.mu.Unlock()
		case <-c.done:
			return
		}
	}
}

func (c *Client) Send(data []byte) error {
	c.sendCh <- data
	return nil
}

func (c *Client) SendMessage(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.Send(data)
}

func (c *Client) Close() {
	close(c.stopCh)
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *Client) OnMessage(fn func(msg *Message)) {
	c.onMessage = fn
}

type SignalingError struct {
	Detail string
}

func (e *SignalingError) Error() string {
	return e.Detail
}

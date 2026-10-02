package signaling

import (
	"crypto/tls"
	"encoding/json"
	"errors"
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
	// P2 §4.5: 本次 offer 宣告的 ICE 等级 1=只放行缓存胜出接口 2=规则过滤 3=不过滤
	IceLevel int `json:"ice_level,omitempty"`
	// P2: md 随 connect_success 下发的阶梯策略(config_json.ice.auto_fallback/path_cache)
	IcePolicy json.RawMessage `json:"ice_policy,omitempty"`
}

type Client struct {
	serverURL          string
	gatewayID          string
	gatewayToken       string
	conn               *websocket.Conn
	mu                 sync.Mutex
	sendCh             chan []byte
	connDone           chan struct{} // 当前连接的生命周期（nil=未连接）；只建一次 per connect
	onMessage          func(msg *Message)
	insecureSkipVerify bool
	stopCh             chan struct{}
	closeOnce          sync.Once
	version            string
	iceServers         []json.RawMessage
}

// Send 侧错误（原实现无 select，writePump 退出后调用方永久阻塞）
var (
	ErrClosed      = errors.New("signaling: client closed")
	ErrSendTimeout = errors.New("signaling: send timeout")
)

// sendWaitTimeout 缓冲满时最多等多久。覆盖典型重连退避(1~8s)，
// 超过即判为真实故障，避免调用方 goroutine 永久挂起。
const sendWaitTimeout = 10 * time.Second

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
		serverURL:    serverURL,
		gatewayID:    gatewayID,
		gatewayToken: gatewayToken,
		// sendCh 全生命周期只建一次：重连时复用，避免旧 writePump 关掉新通道
		sendCh:             make(chan []byte, 64),
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

		connDone, err := c.connect()
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

		<-connDone
		log.Printf("[SIG] connection lost, reconnecting...")
		select {
		case <-c.stopCh:
			return
		case <-time.After(backoff):
		}
	}
}

// connect 建立一次连接。返回本次连接的 connDone（readPump 退出时关闭），
// 供 ConnectLoop 等待“本次连接断开”。done/sendCh 全生命周期只建一次 ——
// 旧实现每次重连都重建，并由旧 readPump 的 close(c.done) 关掉新通道。
func (c *Client) connect() (<-chan struct{}, error) {
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
		return nil, err
	}

	connDone := make(chan struct{})
	c.mu.Lock()
	c.conn = conn
	c.connDone = connDone
	c.mu.Unlock()

	reg := Message{
		Type:      TypeRegisterGateway,
		GatewayID: c.gatewayID,
		Token:     c.gatewayToken,
		Version:   c.version,
	}
	data, _ := json.Marshal(reg)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		conn.Close()
		c.clearConn(connDone)
		return nil, err
	}

	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		c.clearConn(connDone)
		return nil, err
	}
	var resp Message
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		conn.Close()
		c.clearConn(connDone)
		return nil, err
	}
	if resp.Type == TypeRegisterError {
		conn.Close()
		c.clearConn(connDone)
		return nil, &SignalingError{Detail: resp.Error}
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

	go c.readPump(conn, connDone)
	go c.writePump(conn, connDone)
	return connDone, nil
}

// clearConn 仅当仍指向本连接时清空（避免旧 pump 关掉新连接的字段）
func (c *Client) clearConn(connDone chan struct{}) {
	c.mu.Lock()
	if c.connDone == connDone {
		c.connDone = nil
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Client) readPump(conn *websocket.Conn, connDone chan struct{}) {
	defer func() {
		// close 在 clearConn 之后：即便并发 connect() 已换新连接，比较也不会误清新连接
		c.clearConn(connDone)
		close(connDone)
	}()
	for {
		_, data, err := conn.ReadMessage()
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
			conn.WriteMessage(websocket.TextMessage, pongData)
			c.mu.Unlock()
		default:
			if c.onMessage != nil {
				c.onMessage(&msg)
			}
		}
	}
}

func (c *Client) writePump(conn *websocket.Conn, connDone chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg := <-c.sendCh:
			c.mu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				c.mu.Unlock()
				log.Printf("[SIG] write failed: %v", err)
				_ = conn.Close()
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
			if err := conn.WriteMessage(websocket.TextMessage, hb); err != nil {
				c.mu.Unlock()
				log.Printf("[SIG] heartbeat write failed: %v", err)
				_ = conn.Close()
				return
			}
			// 兼容仍监听 ping 的服务端
			ping, _ := json.Marshal(Message{Type: TypePing, GatewayID: c.gatewayID})
			if err := conn.WriteMessage(websocket.TextMessage, ping); err != nil {
				c.mu.Unlock()
				log.Printf("[SIG] ping write failed: %v", err)
				_ = conn.Close()
				return
			}
			c.mu.Unlock()
		case <-connDone:
			return
		}
	}
}

// Send 投递到共享 sendCh。缓冲满时有界等待（10s），绝不永久阻塞调用方 ——
// sendCh 为全生命周期单例，重连后新 writePump 会继续消费，故断线期间的消息仍可送达。
func (c *Client) Send(data []byte) error {
	select {
	case c.sendCh <- data:
		return nil
	default:
	}
	timer := time.NewTimer(sendWaitTimeout)
	defer timer.Stop()
	select {
	case c.sendCh <- data:
		return nil
	case <-c.stopCh:
		return ErrClosed
	case <-timer.C:
		return ErrSendTimeout
	}
}

func (c *Client) SendMessage(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.Send(data)
}

func (c *Client) Close() {
	c.closeOnce.Do(func() { close(c.stopCh) })
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
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

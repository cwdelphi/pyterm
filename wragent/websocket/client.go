package websocket

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message WebSocket消息
type Message struct {
	Type       string          `json:"type"`
	RoomID     string          `json:"room_id,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	From       string          `json:"from,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	SDP        json.RawMessage `json:"sdp,omitempty"`
	Candidate  json.RawMessage `json:"candidate,omitempty"`
	ICEServers json.RawMessage `json:"ice_servers,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
	Timestamp  int64           `json:"timestamp,omitempty"`
}

// Client WebSocket客户端
type Client struct {
	url              string
	token            string
	agentID          string
	conn             *websocket.Conn
	mu               sync.RWMutex
	done             chan struct{}
	reconnectDelay   int
	heartbeatInterval int
	onMessage        func(msg *Message)
	onConnect        func()
	onDisconnect     func()
	setupMode        bool
	setupCh          chan *Message
	onSetupURL       func(url string)
	onSetupToken     func(token string)
	iceServers       []ICEServerConfig
	heartbeatStop    chan struct{}
	lastAckUnix      int64
	missedAcks       int
}

// NewClient 创建新的WebSocket客户端
func NewClient(serverURL, token, agentID string) *Client {
	return &Client{
		url:               serverURL,
		token:             token,
		agentID:           agentID,
		done:              make(chan struct{}),
		reconnectDelay:    5,
		heartbeatInterval: 30,
		heartbeatStop:     make(chan struct{}),
	}
}

// OnMessage 设置消息回调
func (c *Client) OnMessage(handler func(msg *Message)) {
	c.onMessage = handler
}

// OnConnect 设置连接成功回调
func (c *Client) OnConnect(handler func()) {
	c.onConnect = handler
}

// OnDisconnect 设置断开连接回调
func (c *Client) OnDisconnect(handler func()) {
	c.onDisconnect = handler
}

// SetReconnectInterval 动态更新重连间隔
func (c *Client) SetReconnectInterval(seconds int) {
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 60 {
		seconds = 60
	}
	c.mu.Lock()
	c.reconnectDelay = seconds
	c.mu.Unlock()
	log.Printf("[AS-WS] reconnect interval updated to %d s", seconds)
}

// SetHeartbeatInterval 动态更新心跳间隔
func (c *Client) SetHeartbeatInterval(seconds int) {
	if seconds < 5 {
		seconds = 5
	}
	if seconds > 120 {
		seconds = 120
	}
	c.mu.Lock()
	c.heartbeatInterval = seconds
	c.mu.Unlock()
	// 重启心跳循环
	c.restartHeartbeat()
	log.Printf("[AS-WS] heartbeat interval updated to %d s", seconds)
}

func (c *Client) restartHeartbeat() {
	// 停止旧的心跳循环
	select {
	case c.heartbeatStop <- struct{}{}:
	default:
	}
	// 启动新的心跳循环
	go c.heartbeatLoop()
}

// Connect 连接到服务器
func (c *Client) Connect() error {
	u, err := url.Parse(c.url)
	if err != nil {
		return fmt.Errorf("解析URL失败: %w", err)
	}
	q := u.Query()
	q.Set("token", c.token)
	q.Set("agent_id", c.agentID)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.lastAckUnix = time.Now().Unix()
	c.missedAcks = 0
	c.mu.Unlock()

	log.Printf("[AS-WS] connected: %s", c.url)

	go c.readLoop()
	go c.heartbeatLoop()

	if c.onConnect != nil {
		c.onConnect()
	}
	return nil
}

// Close 关闭连接
func (c *Client) Close() {
	close(c.done)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// Send 发送消息
func (c *Client) Send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("未连接")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// SendJSON 发送JSON数据
func (c *Client) SendJSON(msgType string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化数据失败: %w", err)
	}
	return c.Send(&Message{
		Type:      msgType,
		AgentID:   c.agentID,
		Timestamp: time.Now().Unix(),
		Data:      jsonData,
	})
}

// readLoop 读取消息循环
func (c *Client) readLoop() {
	defer func() {
		log.Println("[AS-WS] read loop exited")
		c.handleDisconnect()
	}()

	for {
		select {
		case <-c.done:
			return
		default:
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()
			if conn == nil {
				return
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("[AS-WS] read error: %v", err)
				}
				return
			}
			var msg Message
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("[AS-WS] parse msg failed: %v", err)
				continue
			}
			if c.setupMode && (msg.Type == "setup_url" || msg.Type == "setup_complete") {
				log.Printf("[AS-WS] recv: type=%s setupMode=%v", msg.Type, c.setupMode)
				if msg.Type == "setup_url" && c.onSetupURL != nil {
					var payload struct {
						URL string `json:"url"`
						SID string `json:"sid"`
					}
					if err := json.Unmarshal(msg.Data, &payload); err == nil {
						c.onSetupURL(payload.URL)
					} else {
						log.Printf("[AS-WS] setup_url parse failed: %v, data=%s", err, string(msg.Data))
					}
				} else if msg.Type == "setup_complete" && c.onSetupToken != nil {
					var payload struct {
						Token string `json:"token"`
					}
					if err := json.Unmarshal(msg.Data, &payload); err == nil {
						log.Printf("[AS-WS] received token, length=%d", len(payload.Token))
						c.onSetupToken(payload.Token)
					} else {
						log.Printf("[AS-WS] setup_complete parse failed: %v, data=%s", err, string(msg.Data))
					}
				}
			} else if c.onMessage != nil {
				// R5: 串行处理信令，保证 offer/answer/candidate 时序
				c.onMessage(&msg)
			}
			if msg.Type == "heartbeat_ack" {
				c.mu.Lock()
				c.lastAckUnix = time.Now().Unix()
				c.missedAcks = 0
				c.mu.Unlock()
			}
		}
	}
}

// heartbeatLoop 心跳循环 — 连续无 heartbeat_ack 则断线触发重连+重注册
func (c *Client) heartbeatLoop() {
	c.mu.RLock()
	interval := c.heartbeatInterval
	c.mu.RUnlock()
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	const maxMissed = 3 // ~3 个心跳周期无 ack 视为假活

	for {
		select {
		case <-c.done:
			return
		case <-c.heartbeatStop:
			return
		case <-ticker.C:
			if c.lastAckStale(maxMissed) {
				log.Printf("[AS-WS] no heartbeat_ack for %d intervals, force reconnect", maxMissed)
				c.mu.Lock()
				conn := c.conn
				c.conn = nil
				c.mu.Unlock()
				if conn != nil {
					conn.Close()
				}
				return // readLoop → handleDisconnect → reconnect → OnConnect → register
			}
			c.markHeartbeatSent()
			if err := c.Send(&Message{
				Type:      "heartbeat",
				AgentID:   c.agentID,
				Timestamp: time.Now().Unix(),
			}); err != nil {
				log.Printf("[AS-WS] heartbeat send failed: %v", err)
			}
		}
	}
}

// markHeartbeatSent 记录一次心跳已发送（用于假活检测）
func (c *Client) markHeartbeatSent() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 首次发送时以当前时间为基线，避免刚启动误判
	if c.lastAckUnix == 0 {
		c.lastAckUnix = time.Now().Unix()
	}
	c.missedAcks++
}

// lastAckStale 是否连续多个周期未收到 heartbeat_ack
func (c *Client) lastAckStale(maxMissed int) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.missedAcks < maxMissed {
		return false
	}
	// 兜底: 距上次 ack 超过 maxMissed*interval 也视为假活
	return c.lastAckUnix > 0 && time.Now().Unix()-c.lastAckUnix > int64(maxMissed*c.heartbeatInterval)
}

// handleDisconnect 处理断开连接
func (c *Client) handleDisconnect() {
	if c.onDisconnect != nil {
		c.onDisconnect()
	}
	go c.reconnect()
}

// reconnect 重新连接 — R5: 指数退避 + 抖动
func (c *Client) reconnect() {
	attempt := 0
	for {
		select {
		case <-c.done:
			return
		default:
			c.mu.RLock()
			base := c.reconnectDelay
			c.mu.RUnlock()
			// 指数退避: base * 2^attempt，上限 60s，加 ±20% 抖动
			delay := base
			for i := 0; i < attempt && delay < 60; i++ {
				delay *= 2
			}
			if delay > 60 {
				delay = 60
			}
			jitter := time.Duration(float64(delay)*0.2*(float64(time.Now().UnixNano()%100)/50.0 - 1.0)) * time.Second
			sleepFor := time.Duration(delay)*time.Second + jitter
			if sleepFor < time.Second {
				sleepFor = time.Second
			}
			log.Printf("[AS-WS] reconnecting... (attempt=%d, delay %v)", attempt+1, sleepFor)
			time.Sleep(sleepFor)
			if err := c.Connect(); err != nil {
				log.Printf("[AS-WS] reconnect failed: %v", err)
				attempt++
				continue
			}
			log.Println("[AS-WS] reconnected")
			return
		}
	}
}

// AgentID 获取Agent ID
func (c *Client) AgentID() string { return c.agentID }

// SetToken 设置认证 token
func (c *Client) SetToken(token string) { c.token = token }

// Token 获取认证 token
func (c *Client) Token() string { return c.token }

// SetSetupMode 启用 setup 模式
func (c *Client) SetSetupMode(onURL func(string), onToken func(string)) {
	c.setupMode = true
	c.setupCh = make(chan *Message, 1)
	c.onSetupURL = onURL
	c.onSetupToken = onToken
}

// SendSetupStart 发送 setup_start 消息
func (c *Client) SendSetupStart(agentName string) error {
	data, _ := json.Marshal(map[string]string{
		"type":       "setup_start",
		"agent_id":   c.agentID,
		"agent_name": agentName,
	})
	return c.Send(&Message{
		Type:    "setup_start",
		AgentID: c.agentID,
		Data:    data,
	})
}

// SendConnectTunnel 发送隧道连接请求
func (c *Client) SendConnectTunnel(targetAgentID string, token string) error {
	data, _ := json.Marshal(map[string]string{
		"target_agent_id": targetAgentID,
		"token":          token,
	})
	return c.Send(&Message{
		Type:    "connect_tunnel",
		AgentID: c.agentID,
		Data:    data,
	})
}

// IsConnected 检查是否已连接
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// ICEServerConfig ICE服务器配置
type ICEServerConfig struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// SetICEServers 设置服务端下发的ICE配置
func (c *Client) SetICEServers(servers []ICEServerConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.iceServers = servers
}

// GetICEServers 获取ICE配置
func (c *Client) GetICEServers() []ICEServerConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.iceServers
}

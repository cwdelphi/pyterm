package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/ppy-tools/pyagent/config"
	"github.com/ppy-tools/pyagent/pathcache"
	"github.com/ppy-tools/pyagent/signaling"
)

var upgrader = websocket.Upgrader{
	Subprotocols:      []string{"bearer"},
	CheckOrigin:       func(r *http.Request) bool { return true },
	EnableCompression: true,
	WriteBufferSize:   64 * 1024,
	ReadBufferSize:    64 * 1024,
}

type Session struct {
	ID        string
	RoomID    string
	AgentID   string
	GatewayID string
	Conn      *websocket.Conn
	SendCh    chan []byte
	Done      chan struct{}
	// P1: SendCh 在途字节数（入队 +len / 出队 -len），配合 sendByteQuota 背压。
	// 旧实现只按帧数限流 4096 帧 × VNC 60KB ≈ 240MB 峰值。
	sendBytes   atomic.Int64
	PeerConn    *webrtc.PeerConnection
	DataChannel *webrtc.DataChannel
	pendingMsgs [][]byte
	// P2 #14: 本会话热路径限频日志（DC 未就绪/队列满）
	logRL rateLogger
	// P1: DC 背压待发队列（浏览器→agent 终端帧）
	bpPending [][]byte
	bpBytes   int
	bpDropped uint64
	bpFlush   bool
	// C: answer(remote description) 前到达的 ICE 候选缓冲（与 wragent 侧 pendingCandidates 对齐）
	pendingCandidates []webrtc.ICECandidateInit
	// P2: 三级回退阶梯（方案 §4.5）
	gen           atomic.Int32 // 世代号：每次重建 PC 递增，旧回调据此失效（原子读，避免回调持锁死锁）
	iceLevel      int          // 当前等级 1/2/3
	iceRebuilds   int          // 本会话已重建次数（上限 maxRebuild）
	autoFallback  bool         // 后台 config_json.ice.auto_fallback（缺省开）
	pathCacheOn   bool         // 后台 config_json.ice.path_cache（缺省开）
	answerApplied bool         // 当前世代的 answer 是否已生效（L1 窗口起点）
	connected     bool         // 当前世代是否已选出候选对
	iceL1Timer    *time.Timer  // L1 窗口定时器
	mu            sync.Mutex
}

// sendByteQuota SendCh 字节配额。原按帧数(4096)限流在 VNC 大帧下等于 240MB 峰值，
// 改为字节配额后慢消费者被卡在 DC 回调里形成真实背压（网关 RSS 峰值 240MB → 8MB）。
const sendByteQuota = 8 << 20

// enqueue 投递到 SendCh。block=true 时配额满则等 writePump 消费（VNC/SFTP 不丢帧），
// block=false 时配额或队列满即丢弃（终帧/诊断/广播可丢）。
// 任何情况下都会被 session.Done 打断，绝不永久阻塞 ——
// 原实现存在 session.SendCh <- j 的裸投递，会在 DC 断连瞬间永久挂起 pion 回调。
func (s *Session) enqueue(data []byte, block bool) bool {
	n := int64(len(data))
	// P2 #13: 背压等待复用定时器，不再每 50ms time.After 分配
	poll := time.NewTimer(50 * time.Millisecond)
	poll.Stop()
	defer poll.Stop()
	for {
		if s.sendBytes.Load()+n <= sendByteQuota {
			select {
			case s.SendCh <- data:
				s.sendBytes.Add(n)
				return true
			default:
			}
		}
		if !block {
			return false
		}
		poll.Reset(50 * time.Millisecond)
		select {
		case <-s.Done:
			return false
		case <-poll.C:
		}
	}
}

type Handler struct {
	cfg      *config.Config
	client   *signaling.Client
	sessions map[string]*Session
	// P2 #16: RoomID → 会话索引，answer/候选/广播入口免线扫
	roomToSession map[string]*Session
	mu            sync.RWMutex
	// P2: 路径缓存与阶梯状态（进程级，按 agent 维度）
	pathCache   *pathcache.Cache
	ladderState *ladder
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:           cfg,
		sessions:      make(map[string]*Session),
		roomToSession: make(map[string]*Session),
		pathCache:     pathcache.Default(),
		ladderState:   newLadder(),
	}
}

// bindRoom 绑定 session↔room（P2 #16）：先写 RoomID 字段、后写索引；命中索引即可用。
// 两个锁分段取（session.mu → 释放 → h.mu），与读方 h.mu→session.mu 的顺序不嵌套，无 ABBA。
func (h *Handler) bindRoom(session *Session, roomID string) {
	session.mu.Lock()
	prev := session.RoomID
	session.RoomID = roomID
	session.mu.Unlock()
	h.mu.Lock()
	if prev != "" && prev != roomID && h.roomToSession[prev] == session {
		delete(h.roomToSession, prev)
	}
	h.roomToSession[roomID] = session
	h.mu.Unlock()
}

// sessionByRoom 房间 → 会话：O(1) 索引（校验存活）+ 兜底线扫（索引未覆盖的绑定路径）。
func (h *Handler) sessionByRoom(roomID string) *Session {
	if roomID == "" {
		return nil
	}
	h.mu.RLock()
	s := h.roomToSession[roomID]
	live := false
	if s != nil {
		_, live = h.sessions[s.ID]
	}
	h.mu.RUnlock()
	if live {
		return s
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.sessions {
		c.mu.Lock()
		if c.RoomID == roomID {
			c.mu.Unlock()
			return c
		}
		c.mu.Unlock()
	}
	return nil
}

func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	if err := h.verifyToken(token); err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WSS] upgrade error: %v", err)
		return
	}
	session := &Session{
		ID:        fmt.Sprintf("browser_%d", time.Now().UnixNano()),
		GatewayID: h.cfg.GatewayID,
		Conn:      conn,
		SendCh:    make(chan []byte, 4096),
		Done:      make(chan struct{}),
	}
	h.mu.Lock()
	h.sessions[session.ID] = session
	h.mu.Unlock()
	log.Printf("[WSS] browser connected: %s", session.ID)
	go h.readPump(session)
	go h.writePump(session)
}

func (h *Handler) SetClient(client *signaling.Client) {
	h.client = client
	client.OnMessage(func(msg *signaling.Message) {
		switch msg.Type {
		case signaling.TypeConnectSuccess:
			h.handleConnectSuccess(msg)
		case signaling.TypeAnswer:
			h.handleAnswer(msg)
		case signaling.TypeCandidate:
			h.handleCandidate(msg)
		case signaling.TypeBrowserConnect:
			h.handleBrowserConnect(msg)
		case "upgrade":
			log.Printf("[GW-UPGRADE] 收到升级指令: %s", string(msg.Data))
			go h.handleUpgrade(msg.Data)
		default:
			data, _ := json.Marshal(msg)
			h.mu.RLock()
			for _, session := range h.sessions {
				if !session.enqueue(data, false) {
					session.logRL.Printf("[WSS] SendCh full, dropping message for session=%s", session.ID)
				}
			}
			h.mu.RUnlock()
		}
	})
}

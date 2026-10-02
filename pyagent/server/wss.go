package server

import (
	"crypto/tls"
	"log"
	"net/http"

	"github.com/ppy-tools/pyagent/config"
)

type WSServer struct {
	cfg     *config.Config
	handler *Handler
}

func NewWSServer(cfg *config.Config, handler *Handler) *WSServer {
	return &WSServer{cfg: cfg, handler: handler}
}

func (s *WSServer) Start() error {
	mux := http.NewServeMux()
	// WebSocket 挂 / 与 /ws(兼容根路径调用方), /health 健康检查
	mux.HandleFunc("/", s.handler.HandleWebSocket)
	mux.HandleFunc("/ws", s.handler.HandleWebSocket)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","gateway_id":"` + s.cfg.GatewayID + `"}`))
	})

	addr := s.cfg.Listen
	if addr == "" {
		addr = ":5599"
	}

	log.Printf("[WSS] listening on %s", addr)

	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		srv := &http.Server{
			Addr:    addr,
			Handler: mux,
			// WebSocket(gorilla) 不支持 HTTP/2 升级; 显式禁用 h2(空 TLSNextProto), 保证 WS 走 HTTP/1.1
			TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
		}
		return srv.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	}
	return http.ListenAndServe(addr, mux)
}

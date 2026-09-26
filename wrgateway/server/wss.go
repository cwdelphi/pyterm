package server

import (
	"log"
	"net/http"

	"github.com/ppy-tools/wrgateway/config"
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
		return http.ListenAndServeTLS(addr, s.cfg.TLSCert, s.cfg.TLSKey, mux)
	}
	return http.ListenAndServe(addr, mux)
}

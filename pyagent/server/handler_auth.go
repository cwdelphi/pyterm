// handler_auth.go — 由 handler.go 按域拆分（R1/R2 纯移动，无逻辑变更）。
package server

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// extractToken 浏览器侧 JWT: 优先 Authorization / Sec-WebSocket-Protocol, 回退 query（兼容旧客户端, S7 减少进 access log）
func extractToken(r *http.Request) string {
	if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
		if t := strings.TrimSpace(strings.TrimPrefix(ah, "Bearer ")); t != "" {
			return t
		}
	}
	// Sec-WebSocket-Protocol: "bearer", "<jwt>"
	if sp := r.Header.Get("Sec-WebSocket-Protocol"); sp != "" {
		parts := strings.Split(sp, ",")
		for i := 0; i < len(parts); i++ {
			p := strings.TrimSpace(parts[i])
			if i == 0 && strings.EqualFold(p, "bearer") && i+1 < len(parts) {
				return strings.TrimSpace(parts[i+1])
			}
			if !strings.EqualFold(p, "bearer") && p != "" {
				// 单段协议若本身是 JWT 形态（含 2 个 '.'）也接受
				if strings.Count(p, ".") == 2 {
					return p
				}
			}
		}
	}
	return r.URL.Query().Get("token")
}

// deriveVerifyBase 后端 /api/auth/verify 基址（token 走 Authorization 头，不进 query/log）
func (h *Handler) deriveVerifyBase() string {
	su := h.cfg.ServerURL
	if strings.HasPrefix(su, "wss://") {
		su = "https://" + strings.TrimPrefix(su, "wss://")
	} else if strings.HasPrefix(su, "ws://") {
		su = "http://" + strings.TrimPrefix(su, "ws://")
	}
	idx := strings.Index(su, "/api/")
	if idx > 0 {
		su = su[:idx]
	}
	return su + "/api/auth/verify"
}

// verifyClients 包级共享（P2 #17）：旧实现每次 WS 建连新建 http.Client+Transport，
// 连接池不复用、每连一发 TLS 握手；改为常驻两个（普通/跳过证书校验），连接可 keep-alive。
var (
	verifyClientOnce     sync.Once
	verifyClientNormal   *http.Client
	verifyClientInsecure *http.Client
)

func verifyHTTPClient(insecure bool) *http.Client {
	verifyClientOnce.Do(func() {
		verifyClientNormal = &http.Client{Timeout: 5 * time.Second}
		verifyClientInsecure = &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	})
	if insecure {
		return verifyClientInsecure
	}
	return verifyClientNormal
}

func (h *Handler) verifyToken(token string) error {
	verifyURL := h.deriveVerifyBase()
	client := verifyHTTPClient(h.cfg.InsecureSkipVerify)
	req, err := http.NewRequest(http.MethodGet, verifyURL, nil)
	if err != nil {
		return fmt.Errorf("verify request build failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[WSS] verify token request failed: %v", err)
		return fmt.Errorf("verify request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("[WSS] token verify failed: status=%d body=%s", resp.StatusCode, string(body))
		return fmt.Errorf("token invalid: status %d", resp.StatusCode)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("invalid verify response: %w", err)
	}
	log.Printf("[WSS] token verified for user %s", result["username"])
	return nil
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

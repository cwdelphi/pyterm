package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ppy-tools/wragent/config"
	"github.com/ppy-tools/wragent/logger"
	wrtc "github.com/ppy-tools/wragent/webrtc"
	ws "github.com/ppy-tools/wragent/websocket"
)

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

//go:embed VERSION
var version string

// Optional build-time variables via -ldflags
var (
	buildTime = "unknown"
	gitCommit = "unknown"
)

func init() {
	version = strings.TrimSpace(version)
}

func printVersion() {
	fmt.Printf("wragent %s (commit=%s built=%s go=%s)\n", version, gitCommit, buildTime, runtime.Version())
}

// openBrowser 在默认浏览器中打开 URL
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("无法自动打开浏览器: %v", err)
		log.Printf("请手动访问: %s", url)
	}
}

// runSetupMode Tailscale 风格 Agent 注册流程
func runSetupMode(cfg *config.Config, configPath string) {
	log.Println("检测到未注册的 Agent，进入 Tailscale 风格认证模式")
	log.Println()

	wsClient := ws.NewClient(cfg.ServerURL, "", cfg.AgentID)

	doneCh := make(chan struct{})
	wsClient.SetSetupMode(
		func(url string) {
			fmt.Println()
			fmt.Println("To authenticate, visit:")
			fmt.Printf("  %s\n", url)
			fmt.Println()
			openBrowser(url)
		},
		func(token string) {
			log.Printf("收到认证 token, length=%d", len(token))
			cfg.AuthToken = token
			if err := cfg.Save(configPath); err != nil {
				log.Printf("保存配置失败: %v", err)
			} else {
				log.Printf("配置已保存到 %s", configPath)
			}
			close(doneCh)
		},
	)

	if err := wsClient.Connect(); err != nil {
		log.Fatalf("连接服务器失败: %v", err)
	}
	defer wsClient.Close()

	if err := wsClient.SendSetupStart(cfg.AgentID); err != nil {
		log.Fatalf("发送 setup_start 失败: %v", err)
	}

	log.Println("等待认证完成... (30分钟内有效)")
	<-doneCh
	log.Println("正在以新 token 重新连接...")
	log.Println()
}

// runNormalMode 正常运行模式（含 token 失效自动重认证）
func runNormalMode(cfg *config.Config, configPath string) {
	wrtc.AgentVersion = version
	for {
		log.Printf("wragent 启动 (version=%s)", version)
		log.Printf("Agent ID: %s", cfg.AgentID)
		log.Printf("服务器: %s", cfg.ServerURL)

		wsClient := ws.NewClient(cfg.ServerURL, cfg.AuthToken, cfg.AgentID)
		signalHandler := wrtc.NewSignalHandler(wsClient)

		// 初始化隧道管理器，使 config_update 推送的隧道配置能被热部署
		wrtc.InitTunnelManager(wsClient, signalHandler)

		signalHandler.OnReady(func() {
			log.Println("WebRTC连接准备就绪")
		})

		signalHandler.OnClose(func() {
			log.Println("WebRTC连接关闭")
		})

		authFailed := make(chan struct{})
		signalHandler.OnAuthFailed(func() {
			log.Println("Token 已失效，准备重新认证...")
			cfg.AuthToken = ""
			cfg.Save(configPath)
			close(authFailed)
		})

		signalHandler.OnConnectSuccess(func(roomID string) {
			log.Printf("[TUNNEL] connected, creating peer: room=%s", roomID)
			signalHandler.CreateTunnelPeer(roomID)
		})

		if err := signalHandler.Start(); err != nil {
			log.Fatalf("启动信令处理器失败: %v", err)
		}
		defer signalHandler.Close()

		_ = wsClient

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		select {
		case <-sigChan:
			log.Println("收到中断信号，正在关闭...")
			return
		case <-authFailed:
			log.Println("正在重新认证...")
			var loadErr error
			cfg, loadErr = config.Load(configPath)
			if loadErr != nil {
				log.Fatalf("重新加载配置失败: %v", loadErr)
			}
			runSetupMode(cfg, configPath)
			cfg, loadErr = config.Load(configPath)
			if loadErr != nil {
				log.Fatalf("重新加载配置失败: %v", loadErr)
			}
			log.Println("重新连接中...")
		}
	}
}

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	showVersion := flag.Bool("v", false, "显示版本信息")
	flag.Parse()

	if *showVersion {
		printVersion()
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 模式一: 无 token 且无有效 agent_id 时自动生成并持久化（Tailscale 式节点身份）
	if cfg.AuthToken == "" && (cfg.AgentID == "" || cfg.AgentID == "agent-001") {
		cfg.AgentID = "node-" + randomHex(12)
		if err := cfg.Save(*configPath); err != nil {
			log.Printf("保存自动生成的 Agent ID 失败: %v", err)
		} else {
			log.Printf("已自动生成 Agent ID: %s", cfg.AgentID)
		}
	}

	// 初始化日志级别
	logger.Init(cfg.LogLevel)

	// Host key 校验（S6）: 默认 TOFU，known_hosts 落在 config 同目录
	{
		policy := cfg.HostKeyPolicy
		if policy == "" {
			policy = "tofu"
		}
		kh := cfg.KnownHostsFile
		if kh == "" {
			kh = filepath.Join(filepath.Dir(*configPath), "known_hosts")
		}
		wrtc.InitHostKeyConfig(policy, kh)
		if err := wrtc.HostKeyInitError(); err != nil {
			log.Printf("host key init warning: %v", err)
		}
		log.Printf("SSH host key policy=%s known_hosts=%s", wrtc.HostKeyPolicy(), kh)
	}

	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		if err := cfg.Save(*configPath); err != nil {
			log.Printf("保存默认配置失败: %v", err)
		}
	}

	log.Printf("wragent %s starting...", version)

	if cfg.AuthToken == "" {
		runSetupMode(cfg, *configPath)
		cfg, err = config.Load(*configPath)
		if err != nil {
			log.Fatalf("重新加载配置失败: %v", err)
		}
	}

	// 隧道配置现在从服务端下发，通过 config_update 消息热部署
	// 旧版兼容: 本地 config.json 中的隧道配置仍可使用
	if cfg.TunnelLocalPort > 0 && cfg.TunnelAgentID != "" && cfg.TunnelTargetAddr != "" {
		go wrtc.StartTunnel(cfg)
	}

	runNormalMode(cfg, *configPath)
}

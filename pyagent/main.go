package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	// 内嵌 IANA 时区库：运行时镜像 alpine:3.20 不带 /usr/share/zoneinfo，
	// 而 TZ=Asia/Shanghai 依赖它；体积代价约 200KB(gzip)。
	_ "time/tzdata"

	"github.com/ppy-tools/pyagent/config"
	"github.com/ppy-tools/pyagent/icefilter"
	"github.com/ppy-tools/pyagent/logger"
	"github.com/ppy-tools/pyagent/netinfo"
	"github.com/ppy-tools/pyagent/pathcache"
	"github.com/ppy-tools/pyagent/server"
	"github.com/ppy-tools/pyagent/signaling"
	wrtc "github.com/ppy-tools/pyagent/webrtc"
	ws "github.com/ppy-tools/pyagent/websocket"
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

func printVersion(mode string) {
	fmt.Printf("pyagent %s (mode=%s commit=%s built=%s go=%s)\n", version, mode, gitCommit, buildTime, runtime.Version())
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
		log.Printf("pyagent agent 启动 (version=%s)", version)
		log.Printf("Agent ID: %s", cfg.AgentID)
		log.Printf("服务器: %s", cfg.ServerURL)

		wsClient := ws.NewClient(cfg.ServerURL, cfg.AuthToken, cfg.AgentID)
		signalHandler := wrtc.NewSignalHandler(wsClient)

		// P1: 周期扫描/扫描请求产生的 network_info 走信令通道回传后台
		netinfo.SetReporter(signalHandler.SendNetworkInfo)

		// 初始化隧道管理器，使 config_update 推送的隧道配置能被热部署
		wrtc.InitTunnelManager(wsClient, signalHandler)
		// 隧道看门狗(归属 tunnel 插件): 兜底 connect_tunnel 被拒/DC 未建立时的重试
		wrtc.StartTunnelWatchdog(wsClient, 30*time.Second)

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
			signalHandler.StopPlugins() // 优雅停止 tunnel/socks5 插件监听(阶段D)
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

// startPprof 按需开启 pprof：仅当设置 PYAGENT_PPROF_ADDR 时监听，默认不开启。
// 必须用独立 mux + 独立监听（网关的 wss mux 只暴露 /、/ws、/health），
// 且只允许绑 loopback —— heap/goroutine profile 会泄露会话与地址信息。
// 三个实例同处 host 网络，端口须各自错开（如 6060/6061/6062）。
func startPprof() {
	addr := strings.TrimSpace(os.Getenv("PYAGENT_PPROF_ADDR"))
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("WR_PPROF_ADDR")) // 旧名兼容
	}
	if addr == "" {
		return
	}
	if host, _, err := net.SplitHostPort(addr); err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		log.Printf("[PPROF] WARN: %s 不是 loopback，pprof 面向网络开放有信息泄露风险", addr)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	log.Printf("[PPROF] listening on http://%s/debug/pprof/", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[PPROF] server error: %v", err)
		}
	}()
}

// runAgent 以 Agent 角色启动（原 wragent 逻辑，保持原顺序）
func runAgent(cfg *config.Config, configPath string) {
	// 模式一: 无 token 且无有效 agent_id 时自动生成并持久化（Tailscale 式节点身份）
	if cfg.AuthToken == "" && (cfg.AgentID == "" || cfg.AgentID == "agent-001") {
		cfg.AgentID = "node-" + randomHex(12)
		if err := cfg.Save(configPath); err != nil {
			log.Printf("保存自动生成的 Agent ID 失败: %v", err)
		} else {
			log.Printf("已自动生成 Agent ID: %s", cfg.AgentID)
		}
	}

	logger.Init(cfg.LogLevel)

	// P2 §4.5: 路径缓存落盘目录（与 config.json 同目录；只读时自动退化为纯内存）
	pathcache.SetDir(filepath.Dir(configPath))

	// P0: ICE 接口过滤（docs/传输优化_ICE优化与接口过滤方案_v2.0.md §4.1）
	// 进程级熔断优先级最高；本地自扫描生成规则兜底，后台 config_json.ice 到达后覆盖（P1）
	{
		icefilter.SetEnvGate(cfg.IceInterfaceFilter && netinfo.EnvBool("WRAGENT_ICE_INTERFACE_FILTER", true))
		stopICE := make(chan struct{})
		netinfo.StartRefresher(stopICE, netinfo.Options{
			AllowTailscale: cfg.IceAllowTailscale || netinfo.EnvBool("WRAGENT_ICE_ALLOW_TAILSCALE", false),
		}, 5*time.Minute, func(r *netinfo.Report) {
			log.Printf("[ICE-FILTER] host=%d->%d pairs=%d->%d %s if_hash=%s",
				r.HostBefore, r.HostAfter, r.EstPairsBef, r.EstPairsAft, r.Rule.Summary(), r.IfHash)
			if !r.Valid {
				log.Printf("[ICE-FILTER] warning: %s", r.Reason)
			}
			// P2: 接口集合变化 → 清掉失效路径缓存（Lookup 也会按 if_hash 自动失效）
			if r.Valid {
				if n := pathcache.Default().InvalidateIfHash(r.IfHash); n > 0 {
					log.Printf("[ICE-LADDER] if_hash 变化 → 清理 %d 条路径缓存", n)
				}
			}
		})
	}

	// Host key 校验（S6）: 默认 TOFU，known_hosts 落在 config 同目录
	{
		policy := cfg.HostKeyPolicy
		if policy == "" {
			policy = "tofu"
		}
		kh := cfg.KnownHostsFile
		if kh == "" {
			kh = filepath.Join(filepath.Dir(configPath), "known_hosts")
		}
		wrtc.InitHostKeyConfig(policy, kh)
		if err := wrtc.HostKeyInitError(); err != nil {
			log.Printf("host key init warning: %v", err)
		}
		log.Printf("SSH host key policy=%s known_hosts=%s", wrtc.HostKeyPolicy(), kh)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := cfg.Save(configPath); err != nil {
			log.Printf("保存默认配置失败: %v", err)
		}
	}

	log.Printf("pyagent agent %s starting...", version)

	if cfg.AuthToken == "" {
		runSetupMode(cfg, configPath)
		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			log.Fatalf("重新加载配置失败: %v", err)
		}
	}

	// 隧道配置现在从服务端下发，通过 config_update 消息热部署
	// 旧版兼容: 本地 config.json 中的隧道配置仍可使用
	if cfg.TunnelLocalPort > 0 && cfg.TunnelAgentID != "" && cfg.TunnelTargetAddr != "" {
		go wrtc.StartTunnel(cfg)
	}

	runNormalMode(cfg, configPath)
}

// runGateway 以 Gateway 角色启动（原 wrgateway 逻辑，保持原顺序）
func runGateway(cfg *config.Config, configPath string) {
	logger.Init(cfg.LogLevel)

	// P2: 路径缓存落盘目录（与 config.json 同目录；目录只读时自动退化为纯内存）。
	// handler 必须先于下面的 netinfo 重扫回调创建（回调里要用到 OnNetInfoChanged）。
	pathcache.SetDir(filepath.Dir(configPath))
	handler := server.NewHandler(cfg)

	// P0: ICE 接口过滤（docs/传输优化_ICE优化与接口过滤方案_v2.0.md §4.2）
	// 文件/env 开关（D3）：ice_interface_filter / WRG_ICE_INTERFACE_FILTER
	{
		icefilter.SetEnvGate(cfg.IceInterfaceFilter)
		stopICE := make(chan struct{})
		netinfo.StartRefresher(stopICE, netinfo.Options{
			AllowTailscale: cfg.IceAllowTailscale,
		}, 5*time.Minute, func(r *netinfo.Report) {
			log.Printf("[ICE-FILTER] host=%d->%d pairs=%d->%d %s if_hash=%s",
				r.HostBefore, r.HostAfter, r.EstPairsBef, r.EstPairsAft, r.Rule.Summary(), r.IfHash)
			if !r.Valid {
				log.Printf("[ICE-FILTER] warning: %s", r.Reason)
			}
			// P2: 接口集合变化 → 清理失效路径缓存并回落 L2
			handler.OnNetInfoChanged(r.IfHash)
		})
	}

	if cfg.ServerURL == "" {
		log.Fatal("[MAIN] server_url is required")
	}
	if cfg.GatewayID == "" {
		log.Fatal("[MAIN] gateway_id is required")
	}
	if cfg.GatewayToken == "" {
		log.Fatal("[MAIN] gateway_token is required")
	}

	log.Printf("[MAIN] starting pyagent gateway id=%s version=%s server=%s", cfg.GatewayID, version, cfg.ServerURL)

	client := signaling.NewClient(cfg.ServerURL, cfg.GatewayID, cfg.GatewayToken, cfg.InsecureSkipVerify, version)
	handler.SetClient(client)
	go client.ConnectLoop()

	wss := server.NewWSServer(cfg, handler)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[MAIN] shutting down...")
		client.Close()
		os.Exit(0)
	}()

	if err := wss.Start(); err != nil {
		log.Fatalf("[MAIN] WSS server error: %v", err)
	}
}

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	showVersion := flag.Bool("v", false, "显示版本信息")
	showVersionLong := flag.Bool("version", false, "显示版本信息")
	modeFlag := flag.String("mode", "", `启动模式 "agent"|"gateway"（覆盖配置文件的 mode 字段）`)
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		// -v/-version 必须始终可用（部署脚本用它做下载校验），配置损坏不阻断
		if *showVersion || *showVersionLong {
			m := *modeFlag
			if m == "" {
				m = config.ModeAgent
			}
			printVersion(m)
			return
		}
		log.Fatalf("加载配置失败: %v", err)
	}
	if *modeFlag != "" {
		cfg.Mode = *modeFlag
	}

	if *showVersion || *showVersionLong {
		printVersion(cfg.Mode)
		return
	}

	// T1: 内存/调度旋钮启动调节（agent/gateway 双模式共用），先于 pprof/业务 goroutine
	tuneRuntime()

	// P3 #19: 按需 pprof（长稳/GC 验收的观测前提），两种模式都需要
	startPprof()

	if cfg.IsGateway() {
		runGateway(cfg, *configPath)
		return
	}
	runAgent(cfg, *configPath)
}

// tuneRuntime T1 硬件自适应：env GOMEMLIMIT 显式值永远优先（Go 运行时自动读取，
// 此处只记录不覆盖）；未设置时按 cgroup 限额或宿主 RAM 推导软限：
// min(来源×0.5, 2GiB)，下限 128MiB，debug.SetMemoryLimit 应用。
// GOMAXPROCS 只验证打日志不改值（Go1.25+ cgroup-aware 默认已按配额推导）。
// S8 实测：容器 256MiB 软限下 live set 仅 2–4MB 从未触发，软限定位是 OOM 保险丝，
// 勿改 GOGC 追求降频（GOGC=10 反使 GC 频率 ×2.3、总暂停 ×1.8）。
func tuneRuntime() {
	src := "env"
	limitStr := os.Getenv("GOMEMLIMIT")
	if limitStr == "" {
		if bytes, ok := memoryLimitSource(&src); ok {
			const (
				maxLimit = int64(2) << 30 // 上限 2GiB
				minLimit = int64(128) << 20
				half     = 2
			)
			limit := bytes / half
			if limit > maxLimit {
				limit = maxLimit
			}
			if limit < minLimit {
				limit = minLimit
			}
			debug.SetMemoryLimit(limit)
			limitStr = strconv.FormatInt(limit>>20, 10) + "MiB"
		} else {
			src = "none"
			limitStr = "未设置"
		}
	}
	log.Printf("[TUNE] GOMEMLIMIT=%s(%s) GOMAXPROCS=%d nproc=%d",
		limitStr, src, runtime.GOMAXPROCS(0), runtime.NumCPU())
}

// memoryLimitSource 返回推导 GOMEMLIMIT 的字节来源：cgroup v2 → cgroup v1 → 宿主 RAM。
// src 记录命中层级（cgroup2/cgroup1/ram）。
func memoryLimitSource(src *string) (int64, bool) {
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil { // v2: "max" = 无限制
		s := strings.TrimSpace(string(b))
		if s != "max" {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
				*src = "cgroup2"
				return v, true
			}
		}
	}
	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil { // v1: 无限制≈maxint64
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && v > 0 && v < (1<<50) {
			*src = "cgroup1"
			return v, true
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(ln, "MemTotal:") {
				f := strings.Fields(ln)
				if len(f) >= 2 {
					if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil && kb > 0 {
						*src = "ram"
						return kb * 1024, true
					}
				}
			}
		}
	}
	return 0, false
}

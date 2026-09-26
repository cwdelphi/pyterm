package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/ppy-tools/wrgateway/config"
	"github.com/ppy-tools/wrgateway/logger"
	"github.com/ppy-tools/wrgateway/server"
	"github.com/ppy-tools/wrgateway/signaling"
)

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

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("wrgateway %s (commit=%s built=%s go=%s)\n", version, gitCommit, buildTime, runtime.Version())
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[MAIN] failed to load config: %v", err)
	}

	// 初始化日志级别
	logger.Init(cfg.LogLevel)

	if cfg.ServerURL == "" {
		log.Fatal("[MAIN] server_url is required")
	}
	if cfg.GatewayID == "" {
		log.Fatal("[MAIN] gateway_id is required")
	}
	if cfg.GatewayToken == "" {
		log.Fatal("[MAIN] gateway_token is required")
	}

	log.Printf("[MAIN] starting wrgateway id=%s version=%s server=%s", cfg.GatewayID, version, cfg.ServerURL)

	handler := server.NewHandler(cfg)

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

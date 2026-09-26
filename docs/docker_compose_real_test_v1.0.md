# Docker Compose 真实交互测试环境方案 v1.0

> 日期: 2026-09-10
> 状态: 执行中

## 目标

搭建 Docker Compose 一键部署的真实交互测试环境，覆盖 SSH直连、SFTP客户端、WebRTC终端 三条链路，消除所有 Mock 依赖。

## 架构

```
┌─────────────────────────────────────────────────────┐
│  docker-compose.test.yml                            │
│                                                     │
│  ┌──────────────┐  ┌──────────┐  ┌───────────────┐  │
│  │  md   │  │   sshd   │  │   wragent     │  │
│  │  :5588       │  │   :2222  │  │  (host网络)   │  │
│  │  FastAPI后端  │  │ OpenSSH  │  │  Go WebRTC    │  │
│  └──────┬───────┘  └────┬─────┘  └──────┬────────┘  │
│         │               │               │            │
│  ┌──────┴───────────────┴───────────────┴────────┐  │
│  │            test-runner (Playwright)            │  │
│  │            + pytest L4 集成测试                │  │
│  └───────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

## 三条真实链路

| 链路 | 流向 | 测试内容 |
|------|------|---------|
| SSH 直连 | 浏览器→WebSocket→后端→asyncssh→**sshd容器** | 终端输入输出、resize、多Tab、认证 |
| SFTP 客户端 | 浏览器→HTTP API→后端→asyncssh→**sshd容器** | 目录列出、文件上传下载、重命名删除 |
| WebRTC 终端 | 浏览器→WebSocket→信令→WebRTC→**wragent容器**→本地SSH | Agent注册、终端交互、断线重连 |

## 测试凭据

- SSH/SFTP: `testuser:testpass123` / `root:rootpass123` (sshd容器)
- Web应用: `admin:admin123` (后端注册用户)
- wragent Agent ID: `test-agent-001`

## 文件清单

| 文件 | 用途 |
|------|------|
| `wragent/Dockerfile` | Go 1.24 多阶段构建 |
| `wragent/config/test-config.json` | Agent连接后端的配置 |
| `autotest/docker/sshd/Dockerfile` | OpenSSH测试服务器 |
| `autotest/docker/test-runner/Dockerfile` | Playwright + pytest 运行器 |
| `docker-compose.test.yml` | 编排4个服务 |
| `app/tests/test_integration_real_ssh.py` | L4: asyncssh直连真实SSH |
| `app/tests/test_integration_sftp_client.py` | L4: API直连真实SFTP |

## 风险点

| 风险 | 缓解措施 |
|------|---------|
| wragent需要coturn | coturn用host网络，wragent也用host网络，可互通 |
| WebRTC在Docker内NAT | coturn已配置STUN，Docker host网络绕过NAT |
| Playwright容器无Chrome | 用微软官方 `mcr.microsoft.com/playwright` 镜像 |
| 测试间数据污染 | 每个test用独立用户/连接，teardown清理 |
| sshd容器启动慢 | healthcheck + depends_on condition: service_healthy |
| wragent注册需时间 | 测试前等待Agent在线API返回非空 |

## 历史bug修复

- `asyncssh.FileNotFound` → `asyncssh.SFTPNoSuchFile` (api.py 5处, api_isolated.py 7处)
- SFTP客户端测试mock策略重写 (11个测试全部通过)

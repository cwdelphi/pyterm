# coturn UDP 19302 外部不可达排查记录 v1.1

日期: 2026-09-13
目标: 排查 WebRTC 使用的 coturn UDP 19302 端口为何外部客户端无法访问

## 背景

- 服务器: portal.example.com, 公网 IP 203.0.113.10, 内网 IP 10.2.0.11
- coturn 监听 UDP/TCP 19302 (STUN/TURN), TURNS TCP 5349
- wragent (本机) 可通过所有协议连接; 外部客户端 TCP 5349 正常, UDP 19302 失败
- 腾讯云轻量防火墙: ALL TCP/UDP/ICMP/GRE 全部放通
- 已排查: iptables INPUT ACCEPT 无阻断; nftables 干净; 内核 UDP 无丢包

## 排查结论

| 检查项 | 结果 |
|--------|------|
| 腾讯云控制台防火墙 | ✅ ALL TCP/UDP/ICMP/GRE 全部放通 |
| OS iptables / nftables | ✅ 无任何 UDP 阻断规则 |
| coturn UDP socket 绑定 | ✅ 0.0.0.0:19302 (Docker host 与原生均正常) |
| tcpdump 抓包 eth0 | ✅ STUN 包到达服务器: 203.0.113.11 > 10.2.0.11:19302 |
| Python UDP echo 端口 19302 | ✅ 外部可收到回包 (端口网络层可达) |
| coturn verbose 日志 | ❌ 零条入站 STUN 记录 |
| Docker vs 原生对比 | ❌ 原生 coturn 同样无响应 → 排除 Docker |
| TCP 5349 (TURNS) | ✅ 外部可连接 (wragent 已验证) |
| /proc/net/udp drops | ✅ 0 丢弃 |

## 核心矛盾

STUN 包到达服务器网卡 (tcpdump 可见), 但 coturn 事件循环从未处理
(verbose 无日志、无回复)。原生/Docker 行为一致, 指向 coturn 自身问题。

## 尝试过的修复 (均无效)

1. relay-threads=1 消除重复 socket
2. no-tls / no-dtls / no-cli
3. no-udp-recvmmsg 禁用批量接收
4. 原生运行 coturn (apt 安装), 排除 Docker

## 根因定位 (v1.1)

**coturn 4.18 (latest 镜像) 的 bug：收到外部 UDP 包后不产生响应。**

决定性证据 (strace 附加到 coturn 进程):
- 外部 STUN 包到达服务器 (tcpdump 在 eth0 确认)
- coturn 通过 recvmmsg 成功读取外部包 (fd 19/20, 源 203.0.113.11)
- 但之后 **零次** sendto/sendmsg 调用 —— 没有任何响应尝试
- 本机源 IP (127.0.0.1/10.2.0.11) 的 STUN 请求 coturn 正常响应 (0x0101)

尝试过的配置调整 (4.18 下均无效):
- relay-threads=1 消除重复 socket
- no-tls / no-dtls / no-cli
- no-udp-recvmmsg 禁用批量接收
- 去掉 external-ip
- 原生运行 coturn (apt 安装), 排除 Docker
- 参考 webrtc-tunnel-proxy 配置风格 (simple-log, fingerprint, no-tcp-relay)

## 解决方案 (v1.1 已实施)

**固定 coturn 镜像为 4.6 版本** (docker-compose.yaml: `image: coturn/coturn:4.6`)

验证结果 (coturn 4.6):
- 外部 UDP 19302 STUN Binding → 5/5 成功返回 0x0101
- 外部 TURNS TCP 5349 → TLS 1.3 握手成功, STUN 响应正常
- wragent 连接正常 (服务端动态下发 ICE 配置)

## 状态: 已解决（历史结论，已被取代）

coturn 4.18 曾存在 UDP 外部请求处理问题, 当时降级到 4.6。

**后续决策（2026-09-23）: 统一使用 `coturn/coturn:latest`，不再 pin 4.6。**  
现行镜像策略、中继端口与 TURN 508 排障见: [`coturn_udp_troubleshooting_v1.2.md`](./coturn_udp_troubleshooting_v1.2.md)

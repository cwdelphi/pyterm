# coturn 运维与故障排查 v1.2

日期: 2026-09-23  
目标: 统一 coturn 镜像策略、中继端口规划与 STUN/TURN 测试排障

## 镜像策略（已定）

**统一使用 `coturn/coturn:latest`，不要固定到 4.6。**

- `docker-compose.yaml` → `image: coturn/coturn:latest`
- 早期 v1.1 文档曾因 4.18 外部 UDP 无响应建议 pin 到 4.6；当前环境已用 latest（4.18.x）验证 STUN Binding / TURN Allocate 正常，**以 latest 为准**。
- 若未来 latest 再现「外部 UDP 到达但不回包」，先抓 `verbose` 日志与 tcpdump，再单独开版本排查，**不要擅自改回 pin 版本**。

## 中继端口（Relay Ports）

| 项 | 值 |
|----|-----|
| 范围 | `49160-49259`（**100** 个） |
| 配置 | `config/turnserver.conf` → `min-port` / `max-port` |
| Docker 映射 | `49160-49259:49160-49259/udp` |

**为什么至少 100：** 每个 TURN 会话占用 1 个中继端口。并发 Agent/隧道/测试会话叠加时，41 个端口会耗尽，coturn 返回：

```
create_relay_ioa_sockets: no available ports
ALLOCATE processed, error 508: Cannot create socket
```

浏览器 STUN 测试仍成功（不占中继口），**仅 TURN UDP/TCP 失败**——这是「STUN 过、TURN 挂」的典型根因。

扩容后必须：

1. 改 `turnserver.conf` 的 min/max  
2. 改 `docker-compose.yaml` 端口映射（两边一致）  
3. `docker compose up -d coturn` 重建容器  
4. 验证：`turnutils_uclient -u ... -w ... -p 19302 203.0.113.10` 不再出现 508  

## STUN/TURN 测试弹窗

- 入口：系统管理 → coturn管理 → 「测试」
- 凭证：`GET /api/admin/coturn/{id}/test-credentials`（HMAC，与 `static-auth-secret` 同源）
- 成功条件：STUN → `srflx`；TURN UDP/TCP → `relay`
- 失败详情：展示 `errorText` + `[errorCode]`（如 `... [508]`），不再只显示笼统「ICE候选错误」

### 常见错误码

| 码 | 含义 | 处理 |
|----|------|------|
| 508 | 中继端口耗尽 Cannot create socket | 扩 `min-port`/`max-port` + compose 映射 |
| 401 | 凭证/密钥不匹配 | 核对 DB `coturn_servers.secret` 与 conf `static-auth-secret` |
| 438 | Stale nonce | 通常可自动重试；持续则查 `stale-nonce` |
| 486 | 配额满 | 调 `total-quota` |
| 701 | 分配超时 | 查 UDP/TCP 可达性、防火墙 |

## 排查顺序（以后先做这个）

1. `docker logs pyterm_coturn --since 5m | grep -E '508|no available ports|401|486'`
2. 确认 `ss -ulnp | grep 491` 与 conf 范围一致  
3. `turnutils_stunclient 203.0.113.10 19302`  
4. `turnutils_uclient -p 19302 -u <user> -w <pass> 203.0.113.10`  
5. 再看浏览器测试弹窗的具体 errorCode  

## 历史结论作废说明

`coturn_udp_troubleshooting_v1.0.md` 中「固定 4.6」的结论 **已被本文取代**；镜像继续 `latest`。

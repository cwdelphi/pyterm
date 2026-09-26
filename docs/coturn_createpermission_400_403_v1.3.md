# coturn CreatePermission 400 / 403 排查 v1.3

日期: 2026-09-24
目标: 澄清 `CREATE_PERMISSION` 后的 `error 400` / `error 403` 是否影响 WebRTC 中继

## 结论摘要

| 现象 | 结论 | 影响 |
|------|------|------|
| `CREATE_PERMISSION processed, success` 后紧跟 `error 400: Bad Request`（同 session 毫秒级） | coturn 对**同一大消息内多属性请求**的第二行汇总日志；CP 本体已 success，peer 已记入 | **无影响**（历史约 7676 对，04:06 UTC 后不再出现） |
| `CREATE_PERMISSION processed, error 403: Forbidden IP` | 单次 CP 请求携带**多个 XOR-PEER**，其中部分 IP 命中 coturn 默认拒绝段（loopback / 未显式放行的保留地址等），同请求内其余 peer 仍可 success | **部分 peer 被拒**；同秒可 success+403 并存（统计 mixed=37），ICE 会改走允许的 candidate |
| 日志是否打印被拒 peer IP | **不打印**（仅 success 时有 `peer <ip> lifetime updated`） | 无法从日志直接读出 403 目标 IP |

## 证据

1. **400 与 CP success 成对**
   - `pairs_CP_then_400 = 7676`，`alone_400 = 0`
   - 同毫秒、同 session：`CREATE_PERMISSION processed, success` → `message processed, error 400`
   - 最后出现：`2026-09-24T04:06:00Z`；其后 0 条（近 30 分钟 400=0）

2. **403 为混批拒绝**
   - 同秒同 session success 与 403 并存：`mixed = 37`；`only403 = 24`；`onlyok = 156`
   - 成功 peer 样本（日志可见）：`172.17-22.0.1`、`192.168.*`、`203.0.113.*`、公网地址 —— **无 127.x**
   - 403 突发窗口与 ICE 重协商 / 测试并发一致（06:26、06:44、06:52 UTC）
   - 403 后若同请求内其它 peer 成功，会话仍可用（SSH+FILE E2E 已通过 P2P/relay）

3. **配置现状**（`config/turnserver.conf`）
   - 未配置 `allow-loopback-peers`（coturn 默认拒绝 127.x/::1）
   - 未自定义 `denied-peer-ip`（走版本默认拒绝列表）
   - `no-multicast-peers` 已开

## 处理建议

- **400**：无需处理（已消失，且从不表示 CP 失败）。
- **403**：
  - 一般场景：**可忽略**，属安全默认；ICE 自动改用允许的 candidate。
  - 若确认需要 loopback 中继（本机浏览器↔本机 agent 走 TURN 而非 host），在 conf 增加：
    ```
    allow-loopback-peers
    ```
    后 `docker compose up -d coturn` 重建。
- 排查命令：
  ```bash
  docker logs pyterm_coturn --since 10m | grep -E 'CREATE_PERMISSION|error 40[03]|Forbidden'
  ```

## 关联

- 镜像策略与中继端口：[`coturn_udp_troubleshooting_v1.2.md`](./coturn_udp_troubleshooting_v1.2.md)
- 历史 UDP 4.18 问题：[`coturn_udp_troubleshooting_v1.0.md`](./coturn_udp_troubleshooting_v1.0.md)

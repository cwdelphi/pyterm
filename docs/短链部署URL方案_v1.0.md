# 短链部署URL方案 v1.0

> 日期: 2026-09-28
> 状态: 已实施并通过测试

---

## 一、背景

管理后台「部署 wragent」原本要复制整段部署脚本（约 348 字符）粘到目标机执行。
模式二改为**短链**：签发一个 8 位随机 code，目标机只执行一行：

```
curl -fsSL "https://gx.example.com:5588/a/d/Ab3xK9Qa" | bash
```

约 57 字符，可粘贴、可口述、可微信发送；docker / systemd 两种模式共用同一个 code，由路径区分。

## 二、接口

### 签发短链

```
POST /api/deploy/agent/short        （权限: agent:manage）
Body: {"id": "<agent_id>"}
```

返回：

```json
{
  "code": "Ab3xK9Qa",
  "path_docker": "/a/d/Ab3xK9Qa",
  "path_systemd": "/a/s/Ab3xK9Qa",
  "expires_in": 1800
}
```

- `code` = `secrets.token_urlsafe(6)` → 8 位 `A-Za-z0-9-_`
- 签发时顺带清理已过期记录（惰性过期）

### 拉取脚本（无鉴权，靠 code 时效性）

```
GET /a/d/<code>   → docker 部署脚本（PlainTextResponse, text/x-shellscript）
GET /a/s/<code>   → systemd 部署脚本
```

- mode 不是 `d`/`s` → 404
- code 不存在或已过期 → 404（`{"detail":"deploy link expired or not found"}`）
- **30 分钟内可重复执行**，无单次消费限制
- 脚本内容复用既有 `_generate_docker_script()` / `_generate_systemd_script()`，
  实时按 code 查 Agent 状态取 token，与直接从后台复制的脚本完全一致

> 路由挂在无前缀的 `short_router` 上（`deploy_router` 带 `prefix="/api/deploy"`），
> 并在 `main.py` 的 `app.mount("/", StaticFiles(html=True))` **之前**注册。

## 三、存储

| 表 | 说明 |
|----|------|
| `deploy_links` | `code`(unique, varchar16)、`agent_id`、`exp`(**epoch 秒 int**)、`created_by`、`created_at` |

- 模型：`app/database.py` → `class DeployLink`
- 落 **MariaDB**，`docker restart pyterm_md` 后仍可解析（已实测）
- 不依赖内存/内存映射，不改 nginx

## 四、前端

| 文件 | 改动 |
|------|------|
| `web/src/api.ts` | 新增 `deployAgentShort(agentId)` |
| `web/src/components/AdminPanel.vue` | 部署弹窗「获取短链」：`deployPaths{docker,systemd}`；切换 docker/systemd **不再清空**已获取的短链（`watch(deployMethod)` 仅重置复制态） |
| `web/src/i18n/{zh-CN,en}.json` | `admin.deployStep3` 文案改为「30 分钟内有效、可重复执行」 |

## 五、安全

- 脚本内含 agent token，短链等价于该 token 的 **30 分钟临时暴露窗口**；过期后 code 立即失效
- 签发接口需登录 + `agent:manage`（该权限普通用户同样持有，与既有 `/api/deploy/agent` 语义一致）
- code 熵约 47.6 bit，暴力猜测不可行；伪造 code 只会 404

## 六、测试

- `app/tests/test_deploy_short.py`（TC-DS01~08，14 例含短链+诊断共用套件全绿）
  - 形状/同 code 双模式/可重复执行/docker 脚本/systemd 脚本
  - 非法 mode、伪造 code → 404；未登录 → 401
  - **落库断言**（`deploy_links` 行存在）；人为置过期 → 404
- 实机：签发 → `curl` 两模式均 200 → `docker restart pyterm_md` → 再 curl 仍 200

## 七、改动文件

```
app/database.py        + DeployLink / 迁移
app/auth_api.py        + short_router, POST /api/deploy/agent/short, GET /a/{mode}/{code}
app/main.py            + include_router(short_router)（静态挂载前）
app/tests/test_deploy_short.py   （新增）
web/src/api.ts         + deployAgentShort
web/src/components/AdminPanel.vue
web/src/i18n/{zh-CN,en}.json
```

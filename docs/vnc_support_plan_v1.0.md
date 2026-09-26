# VNC 协议支持方案

> 项目: /ppy_prj/pyterm (泡鱼终端)
> 版本: v1.0
> 日期: 2026-09-13
> 状态: 方案设计

---

## 一、项目背景与目标

### 1.1 现状

| 能力 | 状态 | 说明 |
|------|------|------|
| SSH 终端 | ✅ 已实现 | WebSocket + WebRTC DataChannel 双模式 |
| SFTP 文件管理 | ✅ 已实现 | 嵌入式 SFTP 服务器 + 远程文件浏览器 |
| VNC 远程桌面 | ❌ 未实现 | 前端已有占位按钮 (disabled) |
| RDP 远程桌面 | ❌ 未实现 | 前端已有占位按钮 (disabled) |

### 1.2 目标

在现有 SSH 管理模块中新增 VNC 远程桌面能力:

- 支持直连模式 (服务端直连 VNC Server)
- 支持 Agent 模式 (通过 wragent WebRTC 中继)
- 浏览器内 noVNC 渲染远程桌面
- 多标签页管理 (复用现有 SSH 标签架构)
- 连接配置 CRUD (复用现有 SshConnection 表)

---

## 二、UX 界面设计

### 2.1 整体布局

```
┌─────────────────────────────────────────────────────────────┐
│  TopBar: [文档管理] [网址管理] [SSH管理] [管理面板]          │
├──────────────┬──────────────────────────────────────────────┤
│  侧边栏       │  标签页区域                                  │
│  搜索框       │  ┌──────┬──────┬──────┐                      │
│              │  │终端1 │VNC1 │终端2 │ ← 标签页              │
│  连接列表:    │  └──────┴──────┴──────┘                      │
│  ┌────────┐  │  ┌──────────────────────────────────────────┐│
│  │ 🟢 服务器A│  │                                          ││
│  │   SSH  │  │        VNC 远程桌面视图 (noVNC Canvas)     ││
│  ├────────┤  │                                          ││
│  │ 🟢 服务器B│  └──────────────────────────────────────────┘│
│  │  VNC   │  │  工具栏: [断开][截图][质量:高/中/低][全屏]  │
│  └────────┘  └──────────────────────────────────────────────┘
│  [+] 添加连接                                             │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 连接配置表单

在现有 SshManager 表单基础上扩展:

| 字段 | 类型 | 默认值 | 仅 VNC 显示 |
|------|------|--------|------------|
| 连接类型 | 切换按钮 | `SSH` | - |
| VNC 端口 | 数字输入 | `5900` | ✅ |
| VNC 密码 | 密码输入 | - | ✅ |
| 像素格式 | 下拉 | `tight` | ✅ |
| 色彩深度 | 下拉 | `full(32bpp)` | ✅ |
| 只读模式 | 开关 | `false` | ✅ |

### 2.3 VNC 会话界面

| 区域 | 组件 | 说明 |
|------|------|------|
| 主画面 | `<canvas>` | noVNC 渲染,自适应容器 |
| 顶部工具栏 | 浮动 | 连接状态、延迟、FPS |
| 底部工具栏 | 固定 | 断开/截图/质量/全屏/Ctrl+Alt+Del |
| 加载状态 | Overlay | 连接中/认证中/重连中 |

---

## 三、技术分析

### 3.1 SSH vs VNC 协议对比

| 特性 | SSH 终端 | VNC (RFB) |
|------|---------|-----------|
| 数据类型 | 文本流 (ANSI) | 像素数据 (RGB) |
| 典型带宽 | 0.05-2 Mbps | 3-50+ Mbps |
| 延迟容忍 | <200ms | <80ms 流畅 |
| 浏览器渲染 | xterm.js | noVNC Canvas |
| 编码压缩 | 无 | Tight/ZRLE/JPEG |
| 输入事件 | 键盘 | 键盘+鼠标(坐标) |

### 3.2 RFB 协议流程

```
noVNC (浏览器)                    VNC Server
     │                                │
     │── ProtocolVersion (3.8) ──────>│  版本协商
     │<── ProtocolVersion ────────────│
     │── SecurityType ──────────────>│  安全类型
     │<── SecurityType ──────────────│
     │── Auth Challenge/Response ───>│  认证
     │<── Auth Result ──────────────│
     │── ClientInit (shared) ───────>│
     │<── ServerInit (W×H×name) ────│
     │── SetPixelFormat ────────────>│
     │── SetEncodings ──────────────>│  编码偏好
     │── FramebufferUpdateRequest ──>│  请求帧
     │<── FramebufferUpdate ─────────│  帧数据
     │── KeyEvent / PointerEvent ───>│  用户输入
     │   ...持续更新...                │
```

### 3.3 架构设计

**直连模式:** Browser → WS /api/ws/vnc → Nginx → FastAPI → TCP → VNC Server:5900
**Agent 模式:** Browser → WebRTC DataChannel → wragent (Go) → TCP → VNC Server:5900

### 3.4 DataChannel VNC 协议扩展

| 前缀 | 常量 | 方向 | 载荷 |
|------|------|------|------|
| `0x20` | `MsgVNCConnect` | B→A | JSON: `{host,port,password,pixel_format}` |
| `0x21` | `MsgVNCData` | A→B | 二进制: RFB 帧数据 |
| `0x22` | `MsgVNCInput` | B→A | 二进制: RFB 输入事件 |
| `0x23` | `MsgVNCResize` | B→A | JSON: `{width,height}` |
| `0x24` | `MsgVNCClipboard` | 双向 | 二进制: 剪贴板 |
| `0x2F` | `MsgVNCError` | A→B | JSON: `{detail}` |

---

## 四、多方案对比与推荐

### 4.1 VNC Server 选型

| 特性 | TigerVNC | TurboVNC | x11vnc | TightVNC |
|------|----------|----------|--------|----------|
| 定位 | 通用桌面 | 3D/GPU | 已有X11会话 | 轻量 |
| WebSocket 原生 | ✅ 1.11+ | ❌ | ✅ | ❌ |
| 编码效率 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ |
| 多线程 | ✅ | ✅(更优) | ❌ | ❌ |
| Docker 支持 | ✅ | ✅ | ✅ | 一般 |

**推荐: TigerVNC** — 通用最佳,原生 WebSocket,社区活跃。

### 4.2 前端渲染方案

| 方案 | 性能 | 集成度 | 维护成本 | 推荐 |
|------|------|--------|---------|------|
| A: noVNC 库 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | 低 | ⭐⭐⭐⭐⭐ |
| B: noVNC + websockify | ⭐⭐⭐ | ⭐⭐⭐ | 中 | ⭐⭐⭐ |
| C: Guacamole | ⭐⭐⭐⭐ | ⭐⭐ | 高 | ⭐ |
| D: 自研 RFB | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | 极高 | ⭐ |

**推荐: 方案 A (noVNC 库集成)**

### 4.3 连接架构方案

| 方案 | 带宽效率 | 延迟 | 部署复杂度 | 安全性 | 推荐 |
|------|---------|------|-----------|--------|------|
| A: WebSocket 桥接 | ⭐⭐⭐ | ⭐⭐⭐ | 低 | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| B: WebRTC DataChannel | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | 中 | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| C: websockify 代理 | ⭐⭐⭐ | ⭐⭐⭐ | 中 | ⭐⭐⭐ | ⭐⭐ |

**推荐: 同时支持 A + B** — 复用 SSH 双模式架构。

---

## 五、风险分析

### 5.1 技术风险

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| VNC 帧数据量过大 | 高 | 高 | Tight 编码 + 降低色深 + 质量自适应 |
| 高延迟操作卡顿 | 高 | 中 | 预测性光标 + 降低帧率 + 网络质量提示 |
| noVNC 版本兼容 | 中 | 中 | 锁定版本 + WebSocket 子协议测试 |
| DataChannel 带宽限制 | 中 | 高 | 大帧分片 + 压缩编码 + 降级直连 |

### 5.2 安全风险

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| VNC 密码明文传输 | 高 | 高 | VeNCrypt TLS + WSS + 数据库加密 |
| WebSocket 未授权访问 | 低 | 高 | JWT 验证 + 连接 ID 绑定 |

---

## 六、实施计划

| Phase | 任务 | 估时 |
|-------|------|------|
| Phase 1 | 基础 VNC (直连) — 后端WS桥接 + 前端noVNC + 表单扩展 | 8d |
| Phase 2 | Agent 模式 + 性能优化 — wragent Go桥接 + DataChannel协议 | 8d |
| Phase 3 | 增强功能 — 剪贴板/截图/全屏/Ctrl+Alt+Del | 4d |
| **总计** | | **20 工作日** |

---

## 七、依赖清单

| 层级 | 新增依赖 | 用途 |
|------|---------|------|
| 前端 | `@novnc/novnc ^1.2.0` | RFB 客户端 |
| Go Agent | `github.com/xordspar0/go-vnc` | RFB 客户端 (Agent 模式) |
| Python | (无,用标准库 asyncio.open_connection) | TCP 桥接 |
| 服务端 | TigerVNC 1.11+ | VNC Server (目标机) |

---

## 八、数据库变更 (SshConnection 表)

| 新字段 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `connection_type` | VARCHAR(10) | `'ssh'` | `ssh`/`vnc` |
| `vnc_port` | INT | `5900` | VNC 端口 |
| `vnc_password` | VARCHAR(255) | `''` | VNC 密码 (加密) |
| `pixel_format` | VARCHAR(20) | `'tight'` | 编码格式 |
| `color_depth` | VARCHAR(10) | `'full'` | 色彩深度 |
| `read_only` | BOOLEAN | `false` | 只读模式 |

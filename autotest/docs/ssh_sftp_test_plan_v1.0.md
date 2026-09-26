# SSH/SFTP/WebRTC 专用测试包方案

> 版本: v1.0 | 日期: 2026-09-10 | 状态: 方案设计

---

## 1. 测试目标

针对 SSH/SFTP 功能（直连模式 + WebRTC 模式）建立分层测试体系，覆盖：

| 层级 | 测试类型 | 技术栈 | 覆盖范围 |
|------|----------|--------|----------|
| L1 | 单元测试 | Go testing + testify | wragent Go 组件逻辑 |
| L2 | 后端API测试 | pytest + httpx + WebSocket | Python API 端点 + WebSocket |
| L3 | E2E浏览器测试 | Playwright + Chromium | 用户完整操作流程 |
| L4 | 集成测试 | Playwright + wragent + Docker Compose | wragent ↔ 后端 ↔ 浏览器全链路 |

---

## 2. 测试架构图

### 2.1 四层测试架构总览

```
┌─────────────────────────────────────────────────────────────────────┐
│                        测试架构总览                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─── L4 集成测试 ──────────────────────────────────────────────┐   │
│  │  Playwright + wragent + Docker Compose                       │   │
│  │  浏览器 ──WebRTC──► wragent ──SSH/SFTP──► 本地服务            │   │
│  └───────────────────────────────────────────────────────────────┘   │
│         │                                                           │
│  ┌─── L3 E2E 浏览器测试 ───────────────────────────────────────┐   │
│  │  Playwright + Chromium (headless)                           │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────┐  │   │
│  │  │ SSH直连   │  │ SSH WebRTC│  │ SFTP服务  │  │ Agent管理  │  │   │
│  │  │ 终端测试  │  │ 终端测试  │  │ 生命周期  │  │ 连接测试   │  │   │
│  │  └────┬─────┘  └────┬─────┘  └────┬─────┘  └─────┬──────┘  │   │
│  │       │WebSocket     │WebRTC       │REST          │WebRTC    │   │
│  └───────┼──────────────┼─────────────┼──────────────┼──────────┘   │
│          │              │             │              │               │
│  ┌─── L2 后端API测试 ──────────────────────────────────────────┐   │
│  │  pytest + FastAPI TestClient + WebSocket                     │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────┐  │   │
│  │  │ SSH CRUD │  │ WS SSH   │  │ SFTP服务  │  │ WebRTC     │  │   │
│  │  │ /api/ssh │  │ /api/ws/  │  │ /api/sftp │  │ /api/ws/   │  │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └────────────┘  │   │
│  └───────────────────────────────────────────────────────────────┘   │
│         │                                                           │
│  ┌─── L1 单元测试 (wragent Go) ─────────────────────────────────┐   │
│  │  Go testing + testify + net/http/httptest                    │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────┐  │   │
│  │  │ config   │  │ websocket│  │ webrtc   │  │ ssh/sftp   │  │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └────────────┘  │   │
│  └───────────────────────────────────────────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 连接模式对比

```
┌─────────────────────────────┬─────────────────────────────────────┐
│      直连模式 (Direct)       │     WebRTC模式 (P2P)               │
├─────────────────────────────┼─────────────────────────────────────┤
│  浏览器 ──WebSocket──►      │  浏览器 ──WebSocket──►              │
│         后端Python          │         后端Python (信令)           │
│              │              │              │                      │
│         asyncssh            │         WebRTC Offer/Answer         │
│              │              │              │                      │
│  ◄──SSH PTY──┘              │  ◄──WebRTC DataChannel──►          │
│                             │         wragent (Go)                │
│  Browser ↔ WS ↔ Python     │         ↔ local SSH ↔ Host          │
│        ↔ asyncssh ↔ Host   │                                     │
│  延迟: 中等 (经后端中转)     │  延迟: 低 (P2P直连)                │
└─────────────────────────────┴─────────────────────────────────────┘
```

### 2.3 WebSocket/WebRTC 数据流

```
直连模式数据流:
  ┌──────────┐   stdin    ┌──────────┐   PTY    ┌──────────┐
  │ Browser  │ ────────►  │ FastAPI  │ ──────►  │ Host SSH │
  │ (xterm)  │ ◄────────  │ (asyncssh)│ ◄──────  │ Server   │
  └──────────┘   stdout   └──────────┘          └──────────┘
       │                                                     │
       └────────────── WebSocket (/api/ws/ssh) ─────────────┘

WebRTC模式数据流:
  ┌──────────┐  WebRTC   ┌──────────┐   SSH    ┌──────────┐
  │ Browser  │ ════════► │ wragent  │ ──────►  │ Host SSH │
  │ (xterm)  │ ◄════════ │ (Go)     │ ◄──────  │ Server   │
  └──────────┘ DataChan  └──────────┘          └──────────┘
       │                                                     │
       └──── WebSocket (/api/ws/webrtc) ────────────────────┘
                   (信令通道, 建立后断开)
```

---

## 3. 测试目录结构

```
autotest/
├── docs/
│   └── ssh_sftp_test_plan_v1.0.md
├── tests/
│   ├── helpers.ts
│   ├── ssh-direct/
│   │   ├── ssh-connection.spec.ts
│   │   ├── ssh-terminal.spec.ts
│   │   ├── ssh-auth.spec.ts
│   │   └── ssh-session-cleanup.spec.ts
│   ├── ssh-webrtc/
│   │   ├── webrtc-signaling.spec.ts
│   │   ├── webrtc-terminal.spec.ts
│   │   ├── webrtc-ice.spec.ts
│   │   └── webrtc-reconnect.spec.ts
│   ├── sftp/
│   │   ├── sftp-server.spec.ts
│   │   ├── sftp-client-ops.spec.ts
│   │   ├── sftp-auth.spec.ts
│   │   └── sftp-filebrowser.spec.ts
│   └── agent/
│       ├── agent-registration.spec.ts
│       └── agent-webrtc-connect.spec.ts
├── playwright.config.ts
├── package.json
└── tsconfig.json

wragent/tests/
├── config_test.go
├── websocket_test.go
├── webrtc_peer_test.go
├── webrtc_signal_test.go
├── ssh_server_test.go
└── sftp_server_test.go

app/tests/
├── test_ssh_sftp_api.py
├── test_sftp_client.py
└── test_webrtc_signaling.py
```

---

## 4. L1 单元测试 (wragent Go)

### 4.1 测试用例总表

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| G-01 | config_test.go | TestLoadConfig | 加载配置文件并解析字段 | P0 |
| G-02 | config_test.go | TestDefaultConfig | 缺省配置填充逻辑 | P1 |
| G-03 | config_test.go | TestSaveConfig | 持久化配置写入文件 | P1 |
| G-04 | websocket_test.go | TestWSConnect | WebSocket 连接成功建立 | P0 |
| G-05 | websocket_test.go | TestWSReconnect | 断线后自动重连 | P0 |
| G-06 | websocket_test.go | TestWSHeartbeat | 心跳包发送与超时检测 | P1 |
| G-07 | websocket_test.go | TestWSSend | 消息发送到后端 | P1 |
| G-08 | websocket_test.go | TestWSOnConnect | 连接成功回调触发 | P1 |
| G-09 | websocket_test.go | TestWSOnDisconnect | 断连回调触发 | P1 |
| G-10 | webrtc_peer_test.go | TestCreatePeer | 创建 WebRTCPeer 实例 | P0 |
| G-11 | webrtc_peer_test.go | TestDataChannel | DataChannel 建立与状态 | P0 |
| G-12 | webrtc_peer_test.go | TestBidirData | 双向数据传输 | P0 |
| G-13 | webrtc_peer_test.go | TestPeerClose | Peer 正常关闭 | P1 |
| G-14 | webrtc_peer_test.go | TestPeerManager | PeerManager 管理多个 Peer | P1 |
| G-15 | webrtc_signal_test.go | TestRegister | Agent 向后端注册 | P0 |
| G-16 | webrtc_signal_test.go | TestOffer | 发送 SDP Offer | P0 |
| G-17 | webrtc_signal_test.go | TestAnswer | 接收 SDP Answer | P0 |
| G-18 | webrtc_signal_test.go | TestCandidate | ICE Candidate 交换 | P1 |
| G-19 | ssh_server_test.go | TestSSHStart | SSH 服务启动监听 | P0 |
| G-20 | ssh_server_test.go | TestPtyRequest | PTY 请求分配伪终端 | P0 |
| G-21 | ssh_server_test.go | TestDataBridge | stdin/stdout 数据桥接 | P0 |
| G-22 | ssh_server_test.go | TestSSHStop | SSH 服务优雅关闭 | P1 |
| G-23 | sftp_server_test.go | TestSFTPStart | SFTP 服务启动监听 | P0 |
| G-24 | sftp_server_test.go | TestListDir | 列出目录内容 | P0 |
| G-25 | sftp_server_test.go | TestReadFile | 读取文件内容 | P0 |
| G-26 | sftp_server_test.go | TestWriteFile | 写入文件内容 | P0 |
| G-27 | sftp_server_test.go | TestMkdir | 创建目录 | P1 |
| G-28 | sftp_server_test.go | TestRemove | 删除文件/目录 | P1 |
| G-29 | sftp_server_test.go | TestPathSandbox | 路径沙箱限制在 RootDir | P0 |

### 4.2 Go 测试实现要点

#### 4.2.1 SSH Server 测试

```go
package ssh_server

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func TestSSHStart(t *testing.T) {
	svc, err := NewSSHServ([]byte(testUserKey))
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Start() }()

	time.Sleep(200 * time.Millisecond)

	// 验证监听端口已打开
	conn, err := net.DialTimeout("tcp", svc.Addr(), 2*time.Second)
	require.NoError(t, err)
	conn.Close()

	// 清理
	err = svc.Stop()
	require.NoError(t, err)

	select {
	case err := <-errCh:
		// 优雅退出应返回 nil
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("SSHServ.Stop() 超时未返回")
	}
}

func TestPtyRequest(t *testing.T) {
	svc, _ := NewSSHServ([]byte(testUserKey))
	go svc.Start()
	defer svc.Stop()
	time.Sleep(200 * time.Millisecond)

	// SSH 客户端发起 PTY 请求
	sshCfg := &gossh.ClientConfig{
		User:            "www",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Auth:            []gossh.AuthMethod{gossh.Password("test123")},
	}
	client, err := gossh.Dial("tcp", svc.Addr(), sshCfg)
	require.NoError(t, err)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer session.Close()

	modes := gossh.TerminalModes{
		gossh.ECHO: 1,
		gossh.IGNCR: 0,
	}
	err = session.RequestPty("xterm-256color", 40, 80, modes)
	assert.NoError(t, err)
}
```

#### 4.2.2 SFTP 路径沙箱测试

```go
package sftp_server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathSandbox(t *testing.T) {
	svc, err := NewSFTPServ()
	require.NoError(t, err)

	// RootDir 设为 /tmp/sftp-test
	svc.RootDir = "/tmp/sftp-test"
	err = os.MkdirAll(svc.RootDir, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(svc.RootDir)

	cases := []struct {
		input    string
		expected string
	}{
		{"/foo", "/tmp/sftp-test/foo"},
		{"/foo/bar/baz", "/tmp/sftp-test/foo/bar/baz"},
		{"./foo", "/tmp/sftp-test/foo"},
		{"../etc/passwd", "/tmp/sftp-test/etc/passwd"},
		{"//etc/passwd", "/tmp/sftp-test/etc/passwd"},
	}

	for _, tc := range cases {
		resolved := svc.ResolvePath(tc.input)
		assert.Equal(t, tc.expected, resolved,
			"sandbox path mismatch: input=%s", tc.input)
	}
}
```

---

## 5. L2 后端API测试 (pytest)

### 5.1 测试用例总表

#### 5.1.1 SSH CRUD API

| ID | 测试文件 | 用例名 | 描述 | 优先级 | Mock |
|----|----------|--------|------|--------|------|
| A-01 | test_ssh_sftp_api.py | test_list_connections | 获取连接列表为空/有数据 | P0 | 无 |
| A-02 | test_ssh_sftp_api.py | test_add_connection | 新增 SSH 连接 | P0 | 无 |
| A-03 | test_ssh_sftp_api.py | test_duplicate_connection | 复制已有连接 | P1 | 无 |
| A-04 | test_ssh_sftp_api.py | test_update_password | 更新密码字段 | P0 | 无 |
| A-05 | test_ssh_sftp_api.py | test_delete_connection | 删除连接 | P0 | 无 |
| A-06 | test_ssh_sftp_api.py | test_no_password_field | 响应不含明文密码 | P0 | 无 |
| A-07 | test_ssh_sftp_api.py | test_has_password | 检测密码是否存在 | P1 | 无 |
| A-08 | test_ssh_sftp_api.py | test_user_isolation | 用户间连接隔离 | P0 | 无 |
| A-09 | test_ssh_sftp_api.py | test_validation_errors | 参数校验错误返回 | P1 | 无 |

#### 5.1.2 SSH WebSocket

| ID | 测试文件 | 用例名 | 描述 | 优先级 | Mock |
|----|----------|--------|------|--------|------|
| A-10 | test_ssh_sftp_api.py | test_ws_no_token | 无 token 拒绝连接 | P0 | 无 |
| A-11 | test_ssh_sftp_api.py | test_ws_invalid_token | 无效 token 拒绝连接 | P0 | 无 |
| A-12 | test_ssh_sftp_api.py | test_ws_connect_success | 正常建立 WebSocket 连接 | P0 | asyncssh |
| A-13 | test_ssh_sftp_api.py | test_ws_terminal_data | 接收终端输出数据 | P0 | asyncssh |
| A-14 | test_ssh_sftp_api.py | test_ws_resize | 终端窗口大小调整 | P1 | asyncssh |
| A-15 | test_ssh_sftp_api.py | test_ws_password_lookup | 通过 ID 查询密码 | P0 | 无 |
| A-16 | test_ssh_sftp_api.py | test_ws_connection_lost | SSH 连接丢失处理 | P1 | asyncssh |
| A-17 | test_ssh_sftp_api.py | test_ws_permission_denied | 权限拒绝错误处理 | P1 | asyncssh |
| A-18 | test_ssh_sftp_api.py | test_ws_concurrent | 并发 WebSocket 连接 | P2 | asyncssh |

#### 5.1.3 SSH Test Connection

| ID | 测试文件 | 用例名 | 描述 | 优先级 | Mock |
|----|----------|--------|------|--------|------|
| A-19 | test_ssh_sftp_api.py | test_connection_success | 测试连接成功 | P0 | asyncssh |
| A-20 | test_ssh_sftp_api.py | test_connection_failure | 测试连接失败 | P0 | asyncssh |

#### 5.1.4 SFTP Server

| ID | 测试文件 | 用例名 | 描述 | 优先级 | Mock |
|----|----------|--------|------|--------|------|
| A-21 | test_ssh_sftp_api.py | test_sftp_get_config | 获取 SFTP 配置 | P0 | 无 |
| A-22 | test_ssh_sftp_api.py | test_sftp_save_config | 保存 SFTP 配置 | P0 | 无 |
| A-23 | test_ssh_sftp_api.py | test_sftp_lifecycle | 启动/停止生命周期 | P0 | 无 |
| A-24 | test_ssh_sftp_api.py | test_sftp_status_running | 状态查询(运行中) | P1 | 无 |
| A-25 | test_ssh_sftp_api.py | test_sftp_status_stopped | 状态查询(已停止) | P1 | 无 |
| A-26 | test_ssh_sftp_api.py | test_sftp_logs | 获取 SFTP 日志 | P1 | 无 |
| A-27 | test_ssh_sftp_api.py | test_sftp_double_start | 重复启动处理 | P2 | 无 |

#### 5.1.5 SFTP Client

| ID | 测试文件 | 用例名 | 描述 | 优先级 | Mock |
|----|----------|--------|------|--------|------|
| A-28 | test_sftp_client.py | test_list | 列出目录 | P0 | 无 |
| A-29 | test_sftp_client.py | test_read | 读取文件 | P0 | 无 |
| A-30 | test_sftp_client.py | test_write | 写入文件 | P0 | 无 |
| A-31 | test_sftp_client.py | test_mkdir | 创建目录 | P1 | 无 |
| A-32 | test_sftp_client.py | test_delete | 删除文件 | P0 | 无 |
| A-33 | test_sftp_client.py | test_rename | 重命名文件 | P1 | 无 |
| A-34 | test_sftp_client.py | test_upload | 上传文件 | P0 | 无 |
| A-35 | test_sftp_client.py | test_download | 下载文件 | P0 | 无 |
| A-36 | test_sftp_client.py | test_no_auth | 未认证访问拒绝 | P0 | 无 |
| A-37 | test_sftp_client.py | test_max_size | 超大文件处理 | P2 | 无 |

#### 5.1.6 WebRTC Signaling

| ID | 测试文件 | 用例名 | 描述 | 优先级 | Mock |
|----|----------|--------|------|--------|------|
| A-38 | test_webrtc_signaling.py | test_ice_servers | 获取 ICE 服务器配置 | P0 | 无 |
| A-39 | test_webrtc_signaling.py | test_agents_list | 获取 Agent 列表 | P0 | 无 |
| A-40 | test_webrtc_signaling.py | test_rooms_list | 获取房间列表 | P1 | 无 |
| A-41 | test_webrtc_signaling.py | test_agent_register | Agent 注册 | P0 | 无 |
| A-42 | test_webrtc_signaling.py | test_agent_heartbeat | Agent 心跳 | P1 | 无 |
| A-43 | test_webrtc_signaling.py | test_browser_connect | 浏览器建立信令连接 | P0 | 无 |
| A-44 | test_webrtc_signaling.py | test_offer_answer | SDP Offer/Answer 交换 | P0 | 无 |
| A-45 | test_webrtc_signaling.py | test_ice_candidate | ICE Candidate 交换 | P0 | 无 |
| A-46 | test_webrtc_signaling.py | test_agent_offline | Agent 下线处理 | P1 | 无 |
| A-47 | test_webrtc_signaling.py | test_invalid_token_ws | 无效 token WebSocket | P0 | 无 |
| A-48 | test_webrtc_signaling.py | test_room_cleanup | 房间清理 | P2 | 无 |
| A-49 | test_webrtc_signaling.py | test_concurrent_rooms | 并发房间创建 | P2 | 无 |

### 5.2 Mock 策略

```python
import asyncio
from unittest.mock import AsyncMock, MagicMock, patch
import pytest

class FakeSSHChannel:
    def __init__(self):
        self.data = asyncio.Queue()
        self._closed = False

    async def read(self, n):
        return await self.data.get()

    def write(self, data):
        if not self._closed:
            asyncio.ensure_future(self.data.put(data))

    def close(self):
        self._closed = True


class FakeAsyncSSHProcess:
    def __init__(self):
        self.stdout = FakeSSHChannel()
        self.stdin = FakeSSHChannel()
        self.stderr = FakeSSHChannel()

    async def wait(self):
        return 0


@pytest.fixture
def mock_asyncssh_connection():
    mock_conn = AsyncMock()
    mock_conn.run = AsyncMock(return_value=FakeAsyncSSHProcess())
    mock_conn.open_session = AsyncMock(return_value=FakeSSHChannel())
    mock_conn.close = MagicMock()
    return mock_conn


@pytest.fixture
def mock_ssh_lookup(monkeypatch):
    """Mock _lookup_ssh_connection 避免读取真实 JSON"""
    async def fake_lookup(user_id: str, conn_id: str):
        return {
            "id": conn_id,
            "host": "127.0.0.1",
            "port": 22,
            "username": "testuser",
            "password": "testpass",
        }
    monkeypatch.setattr("app.routers.ssh_ws._lookup_ssh_connection", fake_lookup)
    return fake_lookup


async def test_ws_connect_success(client, mock_ssh_lookup):
    """验证正常建立 WebSocket 终端连接"""
    conn_id = "conn-001"
    async with client.websocket_connect(
        f"/api/ws/ssh/{conn_id}?token=valid-token"
    ) as ws:
        # 接收初始连接成功消息
        msg = await ws.receive_json()
        assert msg["type"] == "connected"
        assert msg["connection"]["id"] == conn_id
```

---

## 6. L3 E2E 浏览器测试 (Playwright)

### 6.1 测试用例总表

#### 6.1.1 SSH 直连终端

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| E-01 | ssh-connection.spec.ts | add_connection | 新增 SSH 连接 | P0 |
| E-02 | ssh-connection.spec.ts | edit_connection | 编辑已有连接 | P0 |
| E-03 | ssh-connection.spec.ts | delete_connection | 删除连接 | P0 |
| E-04 | ssh-connection.spec.ts | test_connection | 测试连接可用性 | P0 |
| E-05 | ssh-terminal.spec.ts | open_terminal | 打开终端标签页 | P0 |
| E-06 | ssh-terminal.spec.ts | terminal_io | 终端输入/输出 | P0 |
| E-07 | ssh-terminal.spec.ts | terminal_resize | 终端窗口缩放 | P1 |
| E-08 | ssh-terminal.spec.ts | multi_tab | 多标签页并发终端 | P1 |
| E-09 | ssh-terminal.spec.ts | close_terminal | 关闭终端标签页 | P1 |
| E-10 | ssh-auth.spec.ts | password_auth | 密码认证成功 | P0 |
| E-11 | ssh-auth.spec.ts | password_auth_failure | 密码错误提示 | P0 |
| E-12 | ssh-session-cleanup.spec.ts | session_cleanup | 断开后会话清理 | P1 |

#### 6.1.2 SSH WebRTC 终端

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| E-13 | webrtc-signaling.spec.ts | agent_list_display | Agent 列表显示 | P0 |
| E-14 | webrtc-signaling.spec.ts | connect_button_state | 连接按钮状态 | P1 |
| E-15 | webrtc-terminal.spec.ts | open_webrtc_terminal | 打开 WebRTC 终端 | P0 |
| E-16 | webrtc-terminal.spec.ts | terminal_io | WebRTC 终端 I/O | P0 |
| E-17 | webrtc-terminal.spec.ts | terminal_resize | WebRTC 终端缩放 | P1 |
| E-18 | webrtc-ice.spec.ts | ice_status_indicator | ICE 连接状态指示 | P1 |
| E-19 | webrtc-reconnect.spec.ts | auto_reconnect | 断线自动重连 | P0 |
| E-20 | webrtc-reconnect.spec.ts | reconnect_restore | 重连后恢复会话 | P1 |

#### 6.1.3 SFTP 服务

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| E-21 | sftp-server.spec.ts | page_load | SFTP 服务页面加载 | P0 |
| E-22 | sftp-server.spec.ts | config_save | 保存 SFTP 配置 | P0 |
| E-23 | sftp-server.spec.ts | lifecycle_start_stop | 启动/停止生命周期 | P0 |
| E-24 | sftp-server.spec.ts | logs_display | 日志输出显示 | P1 |
| E-25 | sftp-server.spec.ts | connect_command | 连接命令复制 | P1 |
| E-26 | sftp-server.spec.ts | status_indicator | 运行状态指示 | P1 |

#### 6.1.4 SFTP 文件管理

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| E-27 | sftp-filebrowser.spec.ts | open_browser | 打开文件管理器 | P0 |
| E-28 | sftp-filebrowser.spec.ts | list_directory | 列出目录内容 | P0 |
| E-29 | sftp-filebrowser.spec.ts | breadcrumb_nav | 面包屑导航 | P1 |
| E-30 | sftp-filebrowser.spec.ts | new_file_dir | 新建文件/目录 | P0 |
| E-31 | sftp-filebrowser.spec.ts | upload_file | 上传文件 | P0 |
| E-32 | sftp-filebrowser.spec.ts | preview_file | 文件预览 | P1 |
| E-33 | sftp-filebrowser.spec.ts | download_file | 下载文件 | P0 |
| E-34 | sftp-filebrowser.spec.ts | rename_file | 重命名文件 | P1 |
| E-35 | sftp-filebrowser.spec.ts | delete_file | 删除文件 | P0 |
| E-36 | sftp-filebrowser.spec.ts | sort_files | 文件排序 | P2 |
| E-37 | sftp-filebrowser.spec.ts | copy_path | 复制路径 | P2 |
| E-38 | sftp-filebrowser.spec.ts | markdown_preview | Markdown 文件预览 | P2 |

#### 6.1.5 Agent 管理

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| E-40 | agent-registration.spec.ts | page_load | Agent 管理页面加载 | P0 |
| E-41 | agent-registration.spec.ts | empty_state | 无 Agent 时空状态 | P1 |
| E-42 | agent-registration.spec.ts | refresh_list | 刷新 Agent 列表 | P1 |
| E-43 | agent-webrtc-connect.spec.ts | webrtc_connect | WebRTC 连接测试 | P0 |
| E-44 | agent-webrtc-connect.spec.ts | terminal_interaction | 终端交互验证 | P0 |

### 6.2 E2E 浏览器操作流程

#### 6.2.1 SSH 直连终端流程

```
步骤 1: 导航到 SSH 管理页
  await page.goto('/ssh')
  await page.waitForSelector('[data-testid="ssh-page"]')

步骤 2: 新增 SSH 连接
  await page.click('[data-testid="btn-add-connection"]')
  await page.fill('#host', '127.0.0.1')
  await page.fill('#port', '22')
  await page.fill('#username', 'www')
  await page.fill('#password', 'test123')
  await page.click('[data-testid="btn-save-connection"]')
  await page.waitForSelector('[data-testid="connection-card"]')

步骤 3: 测试连接
  await page.click('[data-testid="btn-test-connection"]:last-child')
  await page.waitForSelector('[data-testid="test-success"]', { timeout: 10000 })

步骤 4: 打开终端
  await page.click('[data-testid="btn-open-terminal"]:last-child')
  await page.waitForSelector('.xterm-rows')

步骤 5: 执行命令并验证输出
  await page.keyboard.type('echo hello-world\n')
  await page.waitForFunction(
    () => document.querySelector('.xterm-rows')?.textContent?.includes('hello-world'),
    { timeout: 5000 }
  )

步骤 6: 关闭终端
  await page.click('[data-testid="btn-close-terminal"]')
  await page.waitForSelector('.xterm-rows', { state: 'detached' })
```

#### 6.2.2 WebRTC 终端流程

```
步骤 1: 导航到 Agent 管理页
  await page.goto('/agent')
  await page.waitForSelector('[data-testid="agent-page"]')

步骤 2: 确认 Agent 在线
  await page.waitForSelector('[data-testid="agent-online"]', { timeout: 10000 })
  const agentName = await page.textContent('[data-testid="agent-name"]')

步骤 3: 发起 WebRTC 连接
  await page.click('[data-testid="btn-connect-agent"]')
  await page.waitForSelector('.xterm-rows', { timeout: 15000 })

步骤 4: 验证终端 I/O
  await page.keyboard.type('hostname\n')
  await page.waitForFunction(
    () => document.querySelector('.xterm-rows')?.textContent?.length > 5,
    { timeout: 5000 }
  )

步骤 5: 验证 ICE 状态
  const status = await page.textContent('[data-testid="webrtc-ice-status"]')
  expect(status).toMatch(/connected|completed/)
```

#### 6.2.3 SFTP 服务生命周期流程

```
步骤 1: 导航到 SFTP 服务页
  await page.goto('/sftp')
  await page.waitForSelector('[data-testid="sftp-page"]')

步骤 2: 配置 RootDir
  await page.fill('#rootdir', '/data/sftp')
  await page.click('[data-testid="btn-save-config"]')
  await page.waitForSelector('[data-testid="config-saved"]')

步骤 3: 启动服务
  await page.click('[data-testid="btn-start-sftp"]')
  await page.waitForSelector('[data-testid="status-running"]', { timeout: 10000 })

步骤 4: 查看日志
  await page.click('[data-testid="btn-view-logs"]')
  await page.waitForSelector('[data-testid="log-entry"]')
  const logText = await page.textContent('[data-testid="log-entry"]')
  expect(logText).toContain('listening')

步骤 5: 停止服务
  await page.click('[data-testid="btn-stop-sftp"]')
  await page.waitForSelector('[data-testid="status-stopped"]', { timeout: 10000 })

步骤 6: 验证端口释放
  const portInUse = await page.evaluate(async () => {
    try {
      const resp = await fetch('http://127.0.0.1:5589')
      return resp.ok
    } catch { return false }
  })
  expect(portInUse).toBe(false)
```

### 6.3 WebSocket/WebRTC 拦截策略

```typescript
import { test, expect, Page, WebSocket } from '@playwright/test'

interface WSMessage {
  direction: 'send' | 'receive'
  type: string
  data: unknown
  timestamp: number
}

test.describe('SSH WebSocket 拦截测试', () => {
  let wsMessages: WSMessage[] = []

  test.beforeEach(async ({ page }) => {
    wsMessages = []

    // 拦截所有 WebSocket 连接
    page.on('websocket', (ws: WebSocket) => {
      console.log(`[WS] Connected: ${ws.url()}`)

      ws.on('framesent', (frame) => {
        try {
          const data = JSON.parse(frame.payload as string)
          wsMessages.push({
            direction: 'send',
            type: data.type || 'unknown',
            data: data,
            timestamp: Date.now(),
          })
        } catch {
          // 非 JSON 帧
        }
      })

      ws.on('framereceived', (frame) => {
        try {
          const data = JSON.parse(frame.payload as string)
          wsMessages.push({
            direction: 'receive',
            type: data.type || 'unknown',
            data: data,
            timestamp: Date.now(),
          })
        } catch {
          // 非 JSON 帧
        }
      })
    })
  })

  test('should capture terminal data flow', async ({ page }) => {
    await page.goto('/ssh')
    await page.click('[data-testid="btn-open-terminal"]:last-child')
    await page.waitForSelector('.xterm-rows')

    await page.keyboard.type('echo test-pw\n')
    await page.waitForTimeout(2000)

    // 验证捕获到终端数据
    const termData = wsMessages.filter(m =>
      m.type === 'terminal_data' && m.direction === 'receive'
    )
    expect(termData.length).toBeGreaterThan(0)
  })
})

test.describe('WebRTC 信令拦截', () => {
  let signalingMessages: WSMessage[] = []

  test('should capture WebRTC signaling', async ({ page }) => {
    signalingMessages = []

    page.on('websocket', (ws: WebSocket) => {
      if (ws.url().includes('/api/ws/webrtc')) {
        ws.on('framereceived', (frame) => {
          try {
            const data = JSON.parse(frame.payload as string)
            signalingMessages.push({
              direction: 'receive',
              type: data.type,
              data: data,
              timestamp: Date.now(),
            })
          } catch {}
        })

        ws.on('framesent', (frame) => {
          try {
            const data = JSON.parse(frame.payload as string)
            signalingMessages.push({
              direction: 'send',
              type: data.type,
              data: data,
              timestamp: Date.now(),
            })
          } catch {}
        })
      }
    })

    await page.goto('/agent')
    await page.waitForSelector('[data-testid="agent-online"]')
    await page.click('[data-testid="btn-connect-agent"]')

    await page.waitForTimeout(5000)

    // 验证信令流程
    const offers = signalingMessages.filter(m =>
      m.type === 'offer' && m.direction === 'send'
    )
    const answers = signalingMessages.filter(m =>
      m.type === 'answer' && m.direction === 'receive'
    )
    expect(offers.length).toBeGreaterThanOrEqual(1)
    expect(answers.length).toBeGreaterThanOrEqual(1)
  })
})
```

---

## 7. L4 集成测试

### 7.1 wragent + 后端全链路

| ID | 测试文件 | 用例名 | 描述 | 优先级 |
|----|----------|--------|------|--------|
| I-01 | integration.spec.ts | agent_registration | Agent 注册到后端 | P0 |
| I-02 | integration.spec.ts | browser_webrtc_connect | 浏览器通过 WebRTC 连接 Agent | P0 |
| I-03 | integration.spec.ts | terminal_io_e2e | 端到端终端 I/O | P0 |
| I-04 | integration.spec.ts | reconnect_e2e | 断线重连全链路 | P1 |
| I-05 | integration.spec.ts | multi_agent | 多 Agent 同时连接 | P2 |
| I-06 | integration.spec.ts | agent_offline_cleanup | Agent 下线后资源清理 | P1 |

### 7.2 环境准备

```yaml
# docker-compose.test.yaml
version: "3.8"

services:
  backend-test:
    build:
      context: ../app
      dockerfile: Dockerfile
    environment:
      - ENV=testing
      - DATABASE_URL=sqlite:///test.db
      - MD_DIR=/md
      - SSH_WS_SECRET=test-secret-key
    ports:
      - "5588:5588"
    volumes:
      - ../app:/app/app:ro
      - ../md:/md
      - test-data:/data
    healthcheck:
      test: ["CMD", "python", "-c", "import httpx; httpx.get('http://localhost:5588/api/health')"]
      interval: 5s
      timeout: 3s
      retries: 10

  wragent-test:
    build:
      context: ../wragent
      dockerfile: Dockerfile
    environment:
      - BACKEND_URL=ws://backend-test:5588
      - AGENT_TOKEN=test-agent-token
    depends_on:
      backend-test:
        condition: service_healthy
    network_mode: "host"

  ssh-host:
    image: linuxserver/openssh-server
    environment:
      - USER_NAME=testuser
      - USER_PASSWORD=testpass
      - PASSWORD_ACCESS=true
    ports:
      - "2222:2222"

  playwright:
    image: mcr.microsoft.com/playwright:v1.42.0-noble
    working_dir: /tests
    volumes:
      - ./tests:/tests
      - ./playwright.config.ts:/tests/playwright.config.ts
    depends_on:
      backend-test:
        condition: service_healthy
    environment:
      - BASE_URL=http://backend-test:5588
      - SSH_HOST=host.docker.internal
      - SSH_PORT=2222

volumes:
  test-data:
```

---

## 8. 测试数据管理

### 8.1 测试用户

| 用户名 | 密码 | 权限 | 用途 |
|--------|------|------|------|
| admin | admin123 | 管理员 | 全功能测试 |
| viewer | viewer123 | 只读 | 无权限操作验证 |
| testuser | testpass | 普通用户 | SSH 连接测试 |

### 8.2 测试 SSH 连接

| ID | 主机 | 端口 | 用户名 | 密码 | 用途 |
|----|------|------|--------|------|------|
| conn-001 | 127.0.0.1 | 2222 | testuser | testpass | 正常连接测试 |
| conn-002 | 192.168.1.999 | 22 | root | bad | 连接失败测试 |
| conn-003 | 127.0.0.1 | 2222 | www | www123 | 多用户测试 |
| conn-004 | localhost | 2222 | slow | slow | 超时测试 |

### 8.3 清理策略

| 场景 | 清理方式 | 时机 |
|------|----------|------|
| 测试数据库 | 删除并重建 | 每个 test session 开始前 |
| SSH 连接 JSON | 恢复备份文件 | 每个 test session 结束后 |
| SFTP 测试文件 | os.RemoveAll() | fixture teardown |
| WebSocket 连接 | 异常关闭检测 | test fixture teardown |
| Docker 容器 | docker compose down -v | CI pipeline 结束 |

---

## 9. 执行计划

### 9.1 优先级阶段

| 阶段 | 内容 | 测试用例数 | 工期 |
|------|------|-----------|------|
| Phase 1 | L1 Go 核心单元测试 | 15 (P0) | 2天 |
| Phase 2 | L2 SSH CRUD + WebSocket | 15 (P0) | 2天 |
| Phase 3 | L3 SSH 直连 E2E | 10 (P0) | 2天 |
| Phase 4 | L2 SFTP API + Client | 10 (P0) | 1天 |
| Phase 5 | L3 SFTP E2E | 10 (P0) | 1天 |
| Phase 6 | L2/L3 WebRTC 信令 + 终端 | 10 (P0) | 2天 |
| Phase 7 | L4 集成测试 + L1/L2 P1/P2 | 37 (P1+P2+L4) | 2天 |
| **合计** | | **132** | **12天** |

### 9.2 CI 集成

```yaml
# .github/workflows/test.yml
name: SSH/SFTP/WebRTC Tests

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main]

jobs:
  l1-go-unit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - name: Run Go Tests
        working-directory: wragent
        run: go test -v -race -coverprofile=coverage.out ./tests/...
      - name: Upload Coverage
        uses: actions/upload-artifact@v4
        with:
          name: go-coverage
          path: wragent/coverage.out

  l2-pytest-api:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.11"
      - name: Install Dependencies
        run: |
          pip install pytest pytest-asyncio httpx pytest-cov
          pip install -r app/requirements.txt
      - name: Run API Tests
        working-directory: app
        run: pytest tests/ -v --cov=app --cov-report=xml

  l3-e2e-playwright:
    runs-on: ubuntu-latest
    needs: [l2-pytest-api]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "20"
      - name: Install Dependencies
        run: |
          cd autotest
          npm ci
          npx playwright install --with-deps chromium
      - name: Start Services
        run: docker compose -f docker-compose.test.yaml up -d
      - name: Wait for Health
        run: |
          for i in $(seq 1 30); do
            curl -sf http://localhost:5588/api/health && break
            sleep 2
          done
      - name: Run E2E Tests
        run: |
          cd autotest
          npx playwright test --project=chromium
      - name: Upload Report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: playwright-report
          path: autotest/playwright-report/
      - name: Cleanup
        if: always()
        run: docker compose -f docker-compose.test.yaml down -v

  l4-integration:
    runs-on: ubuntu-latest
    needs: [l3-e2e-playwright]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "20"
      - uses: actions/setup-go@v5
        with:
          go-version: "1.22"
      - name: Build wragent
        working-directory: wragent
        run: go build -o wragent ./cmd/wragent
      - name: Start Full Stack
        run: docker compose -f docker-compose.test.yaml up -d
      - name: Run Integration Tests
        run: |
          cd autotest
          npx playwright test --project=integration
      - name: Cleanup
        if: always()
        run: docker compose -f docker-compose.test.yaml down -v
```

---

## 10. 测试覆盖矩阵

| 测试层 | 测试框架 | 用例数 | 覆盖范围 | 执行时间 |
|--------|----------|--------|----------|----------|
| L1 单元测试 | Go testing + testify | 25 | wragent Go 组件 | ~30s |
| L2 后端API测试 | pytest + httpx | 55 | Python API + WebSocket | ~2min |
| L3 E2E浏览器测试 | Playwright + Chromium | 44 | 用户操作流程 | ~8min |
| L4 集成测试 | Playwright + Docker | 8 | 全链路 | ~5min |
| **合计** | | **132** | | **~16min** |

### 10.1 按功能模块分布

| 功能模块 | L1 | L2 | L3 | L4 | 合计 |
|----------|----|----|----|----|------|
| SSH CRUD | 0 | 9 | 4 | 0 | 13 |
| SSH WebSocket/终端 | 4 | 9 | 8 | 2 | 23 |
| WebRTC 信令 | 4 | 12 | 3 | 3 | 22 |
| WebRTC 终端 | 4 | 0 | 5 | 3 | 12 |
| SFTP Server | 7 | 7 | 6 | 0 | 20 |
| SFTP Client | 0 | 10 | 12 | 0 | 22 |
| Agent 管理 | 4 | 4 | 5 | 0 | 13 |
| 路径沙箱/安全 | 2 | 4 | 0 | 0 | 6 |
| **合计** | **25** | **55** | **44** | **8** | **132** |

### 10.2 按优先级分布

| 优先级 | L1 | L2 | L3 | L4 | 合计 | 占比 |
|--------|----|----|----|----|----|------|
| P0 (必须) | 15 | 30 | 25 | 3 | 73 | 55.3% |
| P1 (重要) | 10 | 15 | 15 | 3 | 33 | 25.0% |
| P2 (一般) | 0 | 10 | 4 | 2 | 16 | 12.1% |
| L4 集成 | - | - | - | 8 | 8 | 6.1% |
| **合计** | **25** | **55** | **44** | **8** | **132** | **100%** |

---

> 文档结束 | 生成时间: 2026-09-10 | 本方案随代码迭代持续更新

"""
数据模型定义
"""
from pydantic import BaseModel, Field
from typing import Optional, List
from enum import Enum


class UserRole(str, Enum):
    ADMIN = "admin"
    USER = "user"


# ── 用户认证 ──────────────────────────────────────────

class UserRegister(BaseModel):
    username: str = Field(..., min_length=3, max_length=50, description="用户名")
    password: str = Field(..., min_length=6, max_length=100, description="密码")
    email: str = Field("", description="邮箱")


class UserLogin(BaseModel):
    username: str = Field(..., description="用户名")
    password: str = Field(..., description="密码")


class UserResponse(BaseModel):
    id: str
    username: str
    email: str
    role: str
    avatar: str = ""
    phone: str = ""
    created_at: Optional[str] = None
    last_login: Optional[str] = None
    last_active: Optional[str] = None
    login_count: int = 0
    is_active: bool = True


class TokenResponse(BaseModel):
    token: str
    user: UserResponse


class PasswordChange(BaseModel):
    old_password: str = Field(..., description="旧密码")
    new_password: str = Field(..., min_length=6, max_length=100, description="新密码")


# ── 管理员用户操作 ──────────────────────────────────────

class AdminUserCreate(BaseModel):
    username: str = Field(..., min_length=3, max_length=50)
    password: str = Field(..., min_length=6, max_length=100)
    email: str = ""
    role: str = "user"
    avatar: str = ""
    phone: str = ""


class AdminUserUpdate(BaseModel):
    username: Optional[str] = Field(None, min_length=3, max_length=50)
    email: Optional[str] = None
    role: Optional[str] = None
    avatar: Optional[str] = None
    phone: Optional[str] = None
    is_active: Optional[bool] = None


class AdminUserResetPassword(BaseModel):
    new_password: str = Field(..., min_length=6, max_length=100)


# ── Agent 管理 ──────────────────────────────────────────

class AgentAddReq(BaseModel):
    id: str = Field(..., min_length=3, max_length=50, description="Agent ID")
    name: str = Field(..., min_length=1, max_length=100, description="显示名称")
    coturn_id: Optional[str] = Field(None, description="关联的coturn服务器ID")
    remark: str = Field("", description="备注")


class AgentUpdateReq(BaseModel):
    name: Optional[str] = Field(None, min_length=1, max_length=100, description="显示名称")
    coturn_id: Optional[str] = Field(None, description="关联的coturn服务器ID")
    remark: Optional[str] = Field(None, description="备注")


class AgentTokenReq(BaseModel):
    id: str


class AgentDeleteReq(BaseModel):
    id: str


class GatewayAddReq(BaseModel):
    id: str = Field(..., min_length=3, max_length=50, description="Gateway ID")
    name: str = Field(..., min_length=1, max_length=100, description="显示名称")
    url: str = Field(..., min_length=1, max_length=500, description="网关地址 (wss://...)")
    remark: str = Field("", description="备注")


class GatewayUpdateReq(BaseModel):
    name: Optional[str] = Field(None, min_length=1, max_length=100, description="显示名称")
    url: Optional[str] = Field(None, min_length=1, max_length=500, description="网关地址")
    remark: Optional[str] = Field(None, description="备注")


class AgentStatusReq(BaseModel):
    id: str


# ── SSH 连接 ──────────────────────────────────────────

class SshConn(BaseModel):
    id: Optional[str] = None
    name: str
    host: str
    port: int = 22
    username: str
    auth_type: str = "password"
    password: str = ""
    key_path: str = ""
    connection_mode: str = "agent"
    agent_id: str = ""
    gateway_id: str = ""
    remark: str = ""
    # VNC fields
    connection_type: str = "ssh"
    vnc_port: int = 5900
    vnc_password: str = ""
    pixel_format: str = "tight"
    color_depth: str = "full"
    read_only: bool = False
    # RDP fields (reserved)
    rdp_port: int = 3389
    rdp_password: str = ""
    rdp_domain: str = ""
    rdp_resolution: str = "1920x1080"


class SshDeleteReq(BaseModel):
    id: str


class SshTestReq(BaseModel):
    host: str
    port: int = 22
    username: str
    auth_type: str = "password"
    password: str = ""
    key_path: str = ""


# ── 链接管理 ──────────────────────────────────────────

class WriteFileReq(BaseModel):
    path: str
    content: str


class NewEntryReq(BaseModel):
    parent: str
    name: str
    is_dir: bool = False


class RenameReq(BaseModel):
    old_path: str
    new_name: str


class PathReq(BaseModel):
    path: str


class MoveReq(BaseModel):
    src: str
    dst_dir: str


class LinkItemReq(BaseModel):
    id: Optional[str] = None
    title: str
    url: str
    description: str = ""
    group_id: str = "ungrouped"


class GroupAddReq(BaseModel):
    name: str


class GroupRenameReq(BaseModel):
    id: str
    name: str


class GroupDeleteReq(BaseModel):
    id: str


class LinkDeleteReq(BaseModel):
    id: str


# ── SFTP ──────────────────────────────────────────

class SftpConfigReq(BaseModel):
    share_dir: str
    read_only: bool = True
    username: str = "ppy"
    password: str = "changeme"
    port: int = 2222
    bind: str = "0.0.0.0"


class SftpClientReq(BaseModel):
    host: str
    port: int = 22
    username: str
    auth_type: str = "password"
    password: str = ""
    key_path: str = ""
    path: str = "/"


class SftpWriteReq(SftpClientReq):
    content: str


class SftpMkdirReq(SftpClientReq):
    pass


class SftpRenameReq(SftpClientReq):
    old_path: str
    new_name: str


class SftpChmodReq(SftpClientReq):
    path: str
    mode: str


# ── WebRTC ──────────────────────────────────────────

class WebRTCRegisterReq(BaseModel):
    agent_id: str
    agent_name: str
    agent_version: str = "1.0.0"
    token: str = ""


class WebRTCConnectReq(BaseModel):
    agent_id: str
    conn_type: str = "ssh"


class WebRTCSignalMsg(BaseModel):
    type: str
    room_id: str
    sdp: Optional[str] = None
    candidate: Optional[dict] = None


# ── coturn 管理 ──────────────────────────────────────────

class CoturnServerAdd(BaseModel):
    name: str = Field(..., min_length=1, max_length=100)
    host: str = Field(..., description="IP或域名")
    port: int = 3478
    tls_port: int = 5349
    secret: str = Field(..., description="HMAC密钥")
    realm: str = "pyterm.local"
    relay_range: str = "49160-49259"
    total_quota: int = 100
    remark: str = ""


class CoturnServerUpdate(BaseModel):
    name: Optional[str] = Field(None, min_length=1, max_length=100)
    host: Optional[str] = None
    port: Optional[int] = None
    tls_port: Optional[int] = None
    secret: Optional[str] = None
    realm: Optional[str] = None
    relay_range: Optional[str] = None
    total_quota: Optional[int] = None
    remark: Optional[str] = None
    is_active: Optional[bool] = None


# ── 批量操作 ──────────────────────────────────────────

class BatchDeleteRequest(BaseModel):
    ids: List[str] = Field(..., description="要删除的ID列表")


# ── 审计日志 ──────────────────────────────────────────

class AuditLogResponse(BaseModel):
    id: int
    user_id: str
    username: str
    action: str
    target_type: str = ""
    target_id: str = ""
    detail: str = ""
    ip: str = ""
    created_at: str = ""

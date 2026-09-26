"""
MariaDB database initialization and access via SQLAlchemy 2.0 async ORM.
Tables: users, agents, ssh_connections, login_attempts, links, ssh_keys,
        sftp_configs, coturn_servers, audit_log
"""
import json
import os
import secrets
import time
import bcrypt
from datetime import datetime, timezone
from pathlib import Path

from sqlalchemy import text, func, select, Column, String, Integer, Boolean, Float, Text, ForeignKey, Index, UniqueConstraint
from sqlalchemy.ext.asyncio import create_async_engine, AsyncSession, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship
from sqlalchemy.pool import NullPool

from app.crypto import encrypt_secret


# ════════════════════════════════════════════════════════════
#  ORM Models
# ════════════════════════════════════════════════════════════

class Base(DeclarativeBase):
    pass


class User(Base):
    __tablename__ = "users"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    username: Mapped[str] = mapped_column(String(255), unique=True, nullable=False)
    password_hash: Mapped[str] = mapped_column(String(255), nullable=False)
    email: Mapped[str] = mapped_column(String(255), default="")
    role: Mapped[str] = mapped_column(String(32), default="user")
    avatar: Mapped[str] = mapped_column(String(255), default="")
    phone: Mapped[str] = mapped_column(String(32), default="")
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    last_login: Mapped[str] = mapped_column(String(64), default="")
    last_active: Mapped[str] = mapped_column(String(64), default="")
    login_count: Mapped[int] = mapped_column(Integer, default=0)


class Agent(Base):
    __tablename__ = "agents"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    token: Mapped[str] = mapped_column(String(255), unique=True, nullable=False)
    coturn_id: Mapped[str] = mapped_column(String(64), nullable=True)
    remark: Mapped[str] = mapped_column(Text, default="")
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    owner_id: Mapped[str] = mapped_column(String(64), default="")
    shared_with: Mapped[str] = mapped_column(String(512), default="private")
    conn_type: Mapped[str] = mapped_column(String(32), default="")
    config_json: Mapped[str] = mapped_column(Text, default="{}")
    version: Mapped[str] = mapped_column(String(64), default="")


class Gateway(Base):
    __tablename__ = "gateways"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    token: Mapped[str] = mapped_column(String(255), unique=True, nullable=False)
    url: Mapped[str] = mapped_column(String(512), nullable=False)
    remark: Mapped[str] = mapped_column(Text, default="")
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    owner_id: Mapped[str] = mapped_column(String(64), default="")
    shared_with: Mapped[str] = mapped_column(String(512), default="private")
    version: Mapped[str] = mapped_column(String(64), default="")


class SshConnection(Base):
    __tablename__ = "ssh_connections"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), ForeignKey("users.id", ondelete="CASCADE"), nullable=False)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    host: Mapped[str] = mapped_column(String(255), nullable=False)
    port: Mapped[int] = mapped_column(Integer, default=22)
    username: Mapped[str] = mapped_column(String(255), nullable=False)
    auth_type: Mapped[str] = mapped_column(String(32), default="password")
    password: Mapped[str] = mapped_column(Text, default="")
    key_path: Mapped[str] = mapped_column(Text, default="")
    connection_mode: Mapped[str] = mapped_column(String(32), default="agent")
    agent_id: Mapped[str] = mapped_column(String(64), default="")
    remark: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    # VNC fields
    connection_type: Mapped[str] = mapped_column(String(10), default="ssh")
    vnc_port: Mapped[int] = mapped_column(Integer, default=5900)
    vnc_password: Mapped[str] = mapped_column(Text, default="")
    pixel_format: Mapped[str] = mapped_column(String(20), default="tight")
    color_depth: Mapped[str] = mapped_column(String(10), default="full")
    read_only: Mapped[bool] = mapped_column(Boolean, default=False)
    gateway_id: Mapped[str] = mapped_column(String(64), default="")
    # RDP fields (reserved)
    rdp_port: Mapped[int] = mapped_column(Integer, default=3389)
    rdp_password: Mapped[str] = mapped_column(Text, default="")
    rdp_domain: Mapped[str] = mapped_column(String(255), default="")
    rdp_resolution: Mapped[str] = mapped_column(String(20), default="1920x1080")
    # 拖拽排序
    sort_order: Mapped[int] = mapped_column(Integer, default=0)
    sort_order: Mapped[int] = mapped_column(Integer, default=0)


class Document(Base):
    """文档元数据（目录 + 文件）"""
    __tablename__ = "documents"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), ForeignKey("users.id", ondelete="CASCADE"), nullable=False, index=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    path: Mapped[str] = mapped_column(Text, nullable=False)
    is_dir: Mapped[bool] = mapped_column(Boolean, default=False)
    parent_id: Mapped[str] = mapped_column(String(64), nullable=True)
    size: Mapped[int] = mapped_column(Integer, default=0)
    minio_key: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    updated_at: Mapped[str] = mapped_column(String(64), default="")
    __table_args__ = (UniqueConstraint('user_id', 'path', name='uq_doc_user_path'),)


class LinkGroup(Base):
    """链接分组"""
    __tablename__ = "link_groups"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), ForeignKey("users.id", ondelete="CASCADE"), nullable=False, index=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    sort_order: Mapped[int] = mapped_column(Integer, default=0)
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)


class LinkItem(Base):
    """链接"""
    __tablename__ = "link_items"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), ForeignKey("users.id", ondelete="CASCADE"), nullable=False, index=True)
    group_id: Mapped[str] = mapped_column(String(64), ForeignKey("link_groups.id", ondelete="CASCADE"), nullable=False, index=True)
    title: Mapped[str] = mapped_column(String(255), nullable=False)
    url: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str] = mapped_column(Text, default="")
    sort_order: Mapped[int] = mapped_column(Integer, default=0)
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)


class SshKey(Base):
    __tablename__ = "ssh_keys"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), ForeignKey("users.id", ondelete="CASCADE"), nullable=False)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    private_key: Mapped[str] = mapped_column(Text, nullable=False)
    passphrase: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)


class LoginAttempt(Base):
    __tablename__ = "login_attempts"
    username: Mapped[str] = mapped_column(String(255), primary_key=True)
    count: Mapped[int] = mapped_column(Integer, default=0)
    last_attempt: Mapped[float] = mapped_column(Float, default=0.0)


class SftpConfig(Base):
    __tablename__ = "sftp_configs"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    scope: Mapped[str] = mapped_column(String(32), nullable=False)  # "global" / "per_user"
    user_id: Mapped[str] = mapped_column(String(64), default="")
    share_dir: Mapped[str] = mapped_column(Text, default="")
    read_only: Mapped[bool] = mapped_column(Boolean, default=True)
    username: Mapped[str] = mapped_column(String(255), default="ppy")
    password: Mapped[str] = mapped_column(String(255), default="changeme")
    port: Mapped[int] = mapped_column(Integer, default=2222)
    bind: Mapped[str] = mapped_column(String(64), default="0.0.0.0")


class CoturnServer(Base):
    __tablename__ = "coturn_servers"
    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    name: Mapped[str] = mapped_column(String(255), nullable=False)
    host: Mapped[str] = mapped_column(String(255), nullable=False)
    port: Mapped[int] = mapped_column(Integer, default=3478)
    tls_port: Mapped[int] = mapped_column(Integer, default=5349)
    secret: Mapped[str] = mapped_column(String(255), nullable=False)
    realm: Mapped[str] = mapped_column(String(255), default="pyterm.local")
    relay_range: Mapped[str] = mapped_column(String(64), default="49160-49259")
    max_bps: Mapped[int] = mapped_column(Integer, default=0)
    total_quota: Mapped[int] = mapped_column(Integer, default=100)
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    is_default: Mapped[bool] = mapped_column(Boolean, default=False)
    remark: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    last_health_check: Mapped[str] = mapped_column(String(64), default="")
    health_status: Mapped[str] = mapped_column(String(32), default="unknown")
    owner_id: Mapped[str] = mapped_column(String(64), default="")
    shared_with: Mapped[str] = mapped_column(String(512), default="private")


class AuditLog(Base):
    __tablename__ = "audit_log"
    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    username: Mapped[str] = mapped_column(String(255), nullable=False)
    action: Mapped[str] = mapped_column(String(128), nullable=False)
    target_type: Mapped[str] = mapped_column(String(64), default="")
    target_id: Mapped[str] = mapped_column(String(64), default="")
    detail: Mapped[str] = mapped_column(Text, default="")
    ip: Mapped[str] = mapped_column(String(64), default="")
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)





class ConnectionTimeline(Base):
    __tablename__ = "connection_timeline"
    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    room_id: Mapped[str] = mapped_column(String(128), nullable=False, unique=True)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    conn_name: Mapped[str] = mapped_column(String(255), default="")
    conn_type: Mapped[str] = mapped_column(String(32), nullable=False)
    host: Mapped[str] = mapped_column(String(255), default="")
    port: Mapped[int] = mapped_column(Integer, default=0)
    username: Mapped[str] = mapped_column(String(255), default="")
    path_mode: Mapped[str] = mapped_column(String(32), default="direct")
    client_ip: Mapped[str] = mapped_column(String(64), default="")
    agent_1_ip: Mapped[str] = mapped_column(String(128), default="")
    agent_1_name: Mapped[str] = mapped_column(String(255), default="")
    agent_2_ip: Mapped[str] = mapped_column(String(128), default="")
    agent_2_name: Mapped[str] = mapped_column(String(255), default="")
    gateway_ip: Mapped[str] = mapped_column(String(128), default="")
    agent_id: Mapped[str] = mapped_column(String(64), default="")
    duration_total: Mapped[float] = mapped_column(Float, default=0)
    success: Mapped[int] = mapped_column(Integer, default=1)
    error_stage: Mapped[str] = mapped_column(String(32), default="")
    error_msg: Mapped[str] = mapped_column(Text, default="")
    failed_step: Mapped[int] = mapped_column(Integer, default=0)
    completed_steps: Mapped[int] = mapped_column(Integer, default=0)
    total_steps: Mapped[int] = mapped_column(Integer, default=0)
    browser: Mapped[str] = mapped_column(String(32), default="")
    os_info: Mapped[str] = mapped_column(String(64), default="")
    connected_at: Mapped[str] = mapped_column(String(64), default="")
    created_at: Mapped[str] = mapped_column(String(64), nullable=False)
    updated_at: Mapped[str] = mapped_column(String(64), nullable=False)


class TimelineStep(Base):
    __tablename__ = "timeline_steps"
    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    room_id: Mapped[str] = mapped_column(String(128), nullable=False)
    step_index: Mapped[int] = mapped_column(Integer, nullable=False)
    from_node: Mapped[str] = mapped_column(String(32), nullable=False)
    to_node: Mapped[str] = mapped_column(String(32), nullable=False)
    protocol: Mapped[str] = mapped_column(String(32), nullable=False)
    direction: Mapped[str] = mapped_column(String(16), default="right")
    action: Mapped[str] = mapped_column(String(255), default="")
    description: Mapped[str] = mapped_column(String(500), default="")
    src_ip: Mapped[str] = mapped_column(String(128), default="")
    dst_ip: Mapped[str] = mapped_column(String(128), default="")
    latency_ms: Mapped[float] = mapped_column(Float, default=0)
    latency_pct: Mapped[float] = mapped_column(Float, default=0)
    t_start: Mapped[float] = mapped_column(Float, default=0)
    t_end: Mapped[float] = mapped_column(Float, default=0)
    status: Mapped[str] = mapped_column(String(16), default="ok")
    error_msg: Mapped[str] = mapped_column(String(500), default="")

#  Engine & Session
# ════════════════════════════════════════════════════════════

_DB_HOST = os.environ.get("DB_HOST", "mariadb")
_DB_PORT = int(os.environ.get("DB_PORT", "3306"))
_DB_USER = os.environ.get("DB_USER", "ppy")
_DB_PASS = os.environ.get("DB_PASS", "change_me_pass")
_DB_NAME = os.environ.get("DB_NAME", "ppy_tools")

_engine = None
_session_factory = None


async def init_db():
    """Initialize database engine, create tables, migrate JSON data, ensure admin user."""
    global _engine, _session_factory
    dsn = f"mysql+aiomysql://{_DB_USER}:{_DB_PASS}@{_DB_HOST}:{_DB_PORT}/{_DB_NAME}?charset=utf8mb4"
    if os.environ.get("TESTING"):
        # 测试环境禁用连接池, 避免 anyio 事件循环冲突
        _engine = create_async_engine(dsn, poolclass=NullPool, pool_pre_ping=True)
    else:
        _engine = create_async_engine(dsn, pool_size=10, max_overflow=20, pool_recycle=3600, pool_pre_ping=True)
    _session_factory = async_sessionmaker(_engine, class_=AsyncSession, expire_on_commit=False)

    async with _engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
        # 添加coturn_id字段到agents表（如果不存在）
        try:
            await conn.execute(text("ALTER TABLE agents ADD COLUMN coturn_id VARCHAR(64) NULL"))
        except Exception:
            pass  # 字段已存在
        # 移除coturn_servers表的deploy_method列（如果存在）
        try:
            await conn.execute(text("ALTER TABLE coturn_servers DROP COLUMN deploy_method"))
        except Exception:
            pass  # 列已不存在
        # 添加owner_id和shared_with字段到agents表
        try:
            await conn.execute(text("ALTER TABLE agents ADD COLUMN owner_id VARCHAR(64) DEFAULT ''"))
        except Exception:
            pass
        try:
            await conn.execute(text("ALTER TABLE agents ADD COLUMN shared_with VARCHAR(512) DEFAULT 'private'"))
        except Exception:
            pass
        # 添加conn_type字段到agents表
        try:
            await conn.execute(text("ALTER TABLE agents ADD COLUMN conn_type VARCHAR(32) DEFAULT ''"))
        except Exception:
            pass
        # 添加owner_id和shared_with字段到coturn_servers表
        # 添加config_json字段到agents表
        try:
            await conn.execute(text("ALTER TABLE agents ADD COLUMN config_json TEXT DEFAULT {}"))
        except Exception:
            pass
        # 添加version字段到agents/gateways表
        try:
            await conn.execute(text("ALTER TABLE agents ADD COLUMN version VARCHAR(64) DEFAULT ''"))
        except Exception:
            pass
        try:
            await conn.execute(text("ALTER TABLE gateways ADD COLUMN version VARCHAR(64) DEFAULT ''"))
        except Exception:
            pass
        try:
            await conn.execute(text("ALTER TABLE coturn_servers ADD COLUMN owner_id VARCHAR(64) DEFAULT ''"))
        except Exception:
            pass
        try:
            await conn.execute(text("ALTER TABLE coturn_servers ADD COLUMN shared_with VARCHAR(512) DEFAULT 'private'"))
        except Exception:
            pass
        # VNC fields for ssh_connections table
        for col, sql in [
            ("connection_type", "ALTER TABLE ssh_connections ADD COLUMN connection_type VARCHAR(10) DEFAULT 'ssh'"),
            ("vnc_port", "ALTER TABLE ssh_connections ADD COLUMN vnc_port INT DEFAULT 5900"),
            ("vnc_password", "ALTER TABLE ssh_connections ADD COLUMN vnc_password TEXT DEFAULT NULL"),
            ("pixel_format", "ALTER TABLE ssh_connections ADD COLUMN pixel_format VARCHAR(20) DEFAULT 'tight'"),
            ("color_depth", "ALTER TABLE ssh_connections ADD COLUMN color_depth VARCHAR(10) DEFAULT 'full'"),
            ("read_only", "ALTER TABLE ssh_connections ADD COLUMN read_only BOOLEAN DEFAULT FALSE"),
            ("gateway_id", "ALTER TABLE ssh_connections ADD COLUMN gateway_id VARCHAR(64) DEFAULT ''"),
        ]:
            try:
                await conn.execute(text(sql))
            except Exception:
                pass
        # 创建 gateways 表 (如果不存在)
        # RDP fields for ssh_connections table
        for col, sql in [
            ("rdp_port", "ALTER TABLE ssh_connections ADD COLUMN rdp_port INT DEFAULT 3389"),
            ("rdp_password", "ALTER TABLE ssh_connections ADD COLUMN rdp_password TEXT DEFAULT NULL"),
            ("rdp_domain", "ALTER TABLE ssh_connections ADD COLUMN rdp_domain VARCHAR(255) DEFAULT ''"),
            ("rdp_resolution", "ALTER TABLE ssh_connections ADD COLUMN rdp_resolution VARCHAR(20) DEFAULT ''"),
        ]:
            try:
                await conn.execute(text(sql))
            except Exception:
                pass
        # sort_order for drag-and-drop reordering
        for col, sql in [
            ("sort_order", "ALTER TABLE ssh_connections ADD COLUMN sort_order INT DEFAULT 0"),
        ]:
            try:
                await conn.execute(text(sql))
            except Exception:
                pass
        try:
            await conn.execute(text("""
                CREATE TABLE IF NOT EXISTS gateways (
                    id VARCHAR(64) PRIMARY KEY,
                    name VARCHAR(255) NOT NULL,
                    url VARCHAR(512) NOT NULL,
                    token VARCHAR(255) UNIQUE,
                    remark TEXT DEFAULT '',
                    is_active BOOLEAN DEFAULT TRUE,
                    created_at VARCHAR(64) NOT NULL,
                    owner_id VARCHAR(64) DEFAULT '',
                    shared_with VARCHAR(512) DEFAULT 'private'
                )
            """))
        except Exception:
            pass

    async with _session_factory() as db:
        await _migrate_legacy_data(db)
        await _ensure_admin_user(db)
        await _ensure_default_coturn(db)
        await _ensure_default_vnc_connections(db)
        # 为旧记录设置owner_id（无owner_id的记录归第一个admin所有，优先username='admin'）
        try:
            result = await db.execute(select(User.id).where(User.role == "admin", User.username == "admin").limit(1))
            admin_id = result.scalar_one_or_none()
            if not admin_id:
                result = await db.execute(select(User.id).where(User.role == "admin").limit(1))
                admin_id = result.scalar_one_or_none()
            if admin_id:
                from sqlalchemy import text as _text
                await db.execute(_text(f"UPDATE agents SET owner_id='{admin_id}' WHERE owner_id=''"))
                await db.execute(_text(f"UPDATE coturn_servers SET owner_id='{admin_id}' WHERE owner_id=''"))
                await db.execute(_text(f"UPDATE gateways SET owner_id='{admin_id}' WHERE owner_id=''"))
        except Exception:
            pass
        # 迁移: 将 direct 模式的连接改为 agent（不再创建 local-001 假 Agent）
        try:
            from sqlalchemy import text as _text
            # 将所有 direct 模式连接改为 agent 模式
            await db.execute(_text("UPDATE ssh_connections SET connection_mode='agent' WHERE connection_mode='direct'"))
            # 清理历史遗留的 local-001（若有）及其连接绑定，避免反复出现在 Agent 列表
            result = await db.execute(select(Agent).where(Agent.id == "local-001"))
            if result.scalar_one_or_none():
                await db.execute(_text("UPDATE ssh_connections SET agent_id='' WHERE agent_id='local-001'"))
                await db.execute(_text("DELETE FROM agents WHERE id='local-001'"))
            # 清理测试遗留的空置 wragent-2026092001（无连接引用时）
            result = await db.execute(
                select(SshConnection.id).where(SshConnection.agent_id == "wragent-2026092001").limit(1)
            )
            if not result.scalar_one_or_none():
                await db.execute(_text("DELETE FROM agents WHERE id='wragent-2026092001'"))
            # 检查是否有 local-gateway，没有则创建
            result = await db.execute(select(Gateway).where(Gateway.id == "local-gateway"))
            if not result.scalar_one_or_none():
                import secrets as _secrets
                db.add(Gateway(
                    id="local-gateway", name="本地网关",
                    token=_secrets.token_hex(32),
                    url="wss://203.0.113.10:5599",
                    remark="系统自动生成的本地网关", is_active=True,
                    created_at=datetime.now(timezone.utc).isoformat(),
                ))
                await db.flush()
        except Exception:
            pass
        try:
            result = await db.execute(select(CoturnServer).where(CoturnServer.id == "coturn_default"))
            c = result.scalar_one_or_none()
            if c and c.host in ("192.0.2.50", "192.0.2.50/192.0.2.50"):
                c.host = "203.0.113.10"
        except Exception:
            pass
        await db.commit()


async def close_db():
    global _engine
    if _engine:
        await _engine.dispose()
        _engine = None


async def get_db():
    """Yield an AsyncSession (FastAPI dependency)."""
    if _session_factory is None:
        await init_db()
    async with _session_factory() as session:
        yield session


# ════════════════════════════════════════════════════════════
#  Helpers
# ════════════════════════════════════════════════════════════

def _hash_password(password: str) -> str:
    return bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt(12)).decode("utf-8")


async def _ensure_admin_user(db: AsyncSession):
    """Create default admin user if none exists."""
    result = await db.execute(select(User.id).where(User.role == "admin").limit(1))
    if result.scalar_one_or_none():
        return

    admin_id = f"usr_{secrets.token_hex(4)}"
    now = datetime.now(timezone.utc).isoformat()
    db.add(User(
        id=admin_id, username="admin",
        password_hash=_hash_password("change_me_pass"),
        email="", role="admin", is_active=True,
        created_at=now,
    ))


async def _ensure_default_coturn(db: AsyncSession):
    """Insert default coturn server if none exists."""
    result = await db.execute(select(CoturnServer.id).limit(1))
    if result.scalar_one_or_none():
        return
    now = datetime.now(timezone.utc).isoformat()
    db.add(CoturnServer(
        id="coturn_default",
        name="默认coturn",
        host="203.0.113.10",
        port=19302,
        tls_port=5349,
        secret="change_me_turn",
        realm="pyterm.local",
        relay_range="49160-49259",
        total_quota=100,
        is_active=True,
        is_default=True,
        remark="默认coturn服务器",
        created_at=now,
    ))


async def _ensure_default_vnc_connections(db: AsyncSession):
    """为 admin 用户创建两个默认 VNC 连接（本地网关模式）。
    复制已有的「本地Agent」和「远程Agent」VNC 配置，仅添加 gateway_id。
    """
    result = await db.execute(select(User.id).where(User.username == "admin").limit(1))
    admin_id = result.scalar_one_or_none()
    if not admin_id:
        return

    # 幂等检查
    result = await db.execute(
        select(SshConnection.id).where(
            SshConnection.user_id == admin_id,
            SshConnection.name == "VNC-本地网关+本地Agent"
        ).limit(1)
    )
    if result.scalar_one_or_none():
        return

    now = datetime.now(timezone.utc).isoformat()

    # 查找已有的 VNC 本地Agent 和远程Agent 配置作为参考
    result = await db.execute(
        select(SshConnection).where(
            SshConnection.user_id == admin_id,
            SshConnection.connection_type == "vnc",
        )
    )
    ref_conns = {c.name: c for c in result.scalars().all()}

    ref_local = ref_conns.get("本地Agent")
    ref_remote = ref_conns.get("远程Agent")

    if ref_local:
        db.add(SshConnection(
            id="vnc_lg_la", user_id=admin_id,
            name="VNC-本地网关+本地Agent",
            host=ref_local.host, port=ref_local.port,
            username=ref_local.username, auth_type=ref_local.auth_type,
            password=encrypt_secret(ref_local.password) if ref_local.password and not str(ref_local.password).startswith("enc:v1:") else ref_local.password,
            connection_mode=ref_local.connection_mode,
            agent_id=ref_local.agent_id,
            gateway_id="local-gateway",
            connection_type="vnc",
            vnc_port=ref_local.vnc_port,
            vnc_password=encrypt_secret(ref_local.vnc_password) if ref_local.vnc_password and not str(ref_local.vnc_password).startswith("enc:v1:") else ref_local.vnc_password,
            pixel_format=ref_local.pixel_format, color_depth=ref_local.color_depth,
            read_only=ref_local.read_only, remark="远程管理",
            created_at=now,
        ))

    if ref_remote:
        db.add(SshConnection(
            id="vnc_lg_ra", user_id=admin_id,
            name="VNC-本地网关+远程Agent",
            host=ref_remote.host, port=ref_remote.port,
            username=ref_remote.username, auth_type=ref_remote.auth_type,
            password=encrypt_secret(ref_remote.password) if ref_remote.password and not str(ref_remote.password).startswith("enc:v1:") else ref_remote.password,
            connection_mode=ref_remote.connection_mode,
            agent_id=ref_remote.agent_id,
            gateway_id="local-gateway",
            connection_type="vnc",
            vnc_port=ref_remote.vnc_port,
            vnc_password=encrypt_secret(ref_remote.vnc_password) if ref_remote.vnc_password and not str(ref_remote.vnc_password).startswith("enc:v1:") else ref_remote.vnc_password,
            pixel_format=ref_remote.pixel_format, color_depth=ref_remote.color_depth,
            read_only=ref_remote.read_only, remark="远程管理",
            created_at=now,
        ))

    await db.flush()


# ════════════════════════════════════════════════════════════
#  Legacy → MariaDB migration (from JSON files AND old SQLite)
# ════════════════════════════════════════════════════════════

def _legacy_sqlite_path() -> Path:
    """旧版 SQLite 数据库路径"""
    p = Path(os.environ.get("DB_PATH", str(Path(__file__).resolve().parent.parent / "md" / "data" / "app.db")))
    return p


def _load_legacy_sqlite() -> dict:
    """读取旧版 SQLite 数据 (users/agents/ssh/links)"""
    import sqlite3
    result = {"users": [], "agents": [], "ssh": [], "links": []}
    p = _legacy_sqlite_path()
    if not p.exists():
        return result
    try:
        conn = sqlite3.connect(str(p))
        conn.row_factory = sqlite3.Row
        try:
            for row in conn.execute("SELECT * FROM users"):
                result["users"].append(dict(row))
            for row in conn.execute("SELECT * FROM agents"):
                result["agents"].append(dict(row))
            for row in conn.execute("SELECT * FROM ssh_connections"):
                result["ssh"].append(dict(row))
            for row in conn.execute("SELECT * FROM links"):
                result["links"].append(dict(row))
        except Exception:
            pass
        conn.close()
    except Exception:
        pass
    return result


def _migrate_db_row(model, row: dict) -> object:
    """将旧SQLite行转换为ORM对象"""
    return model(**row)


async def _migrate_legacy_data(db: AsyncSession):
    """从 JSON 文件 + 旧 SQLite 一次性迁移到 MariaDB。
    仅在 users 表为空时执行；保留原始用户ID以维持数据关联。
    """
    result = await db.execute(select(func.count()).select_from(User))
    if result.scalar() > 0:
        return  # already migrated

    base = Path(__file__).resolve().parent.parent / "md"
    shared = base / "_shared"
    now_str = datetime.now(timezone.utc).isoformat()

    # 1) 从旧 SQLite 读取已注册用户/agent (优先保留原ID)
    legacy = _load_legacy_sqlite()
    legacy_users = {u["username"]: u for u in legacy["users"]}

    # 2) 从 users.json 读取用户 (若无旧SQLite)
    users_json = shared / "users.json"
    json_users = []
    if users_json.exists():
        try:
            with open(users_json, "r") as f:
                users_data = json.load(f)
            json_users = users_data.get("users", []) if isinstance(users_data, dict) else users_data
        except Exception:
            pass

    # 合并用户源 (旧SQLite优先)
    all_users = list(legacy_users.values())
    for u in json_users:
        if u.get("username") not in legacy_users:
            all_users.append(u)

    for u in all_users:
        username = u.get("username", "")
        if not username:
            continue
        uid = u.get("id", f"usr_{secrets.token_hex(4)}")
        pwd_hash = u.get("password_hash") or bcrypt.hashpw(u.get("password", "123456").encode(), bcrypt.gensalt()).decode()
        db.add(User(
            id=uid, username=username, password_hash=pwd_hash,
            email=u.get("email", ""), role=u.get("role", "user"),
            is_active=u.get("is_active", True),
            created_at=u.get("created_at", now_str),
            last_login=u.get("last_login", ""),
        ))
        # 立即 flush, 确保用户先于其关联数据落库 (避免外键约束失败)
        await db.flush()

        # 每用户的 SSH 连接: 从旧SQLite + 用户目录JSON
        # get_user_data_dir 始终拼接 "usr_" 前缀, 因此目录为 users/usr_{id}
        user_dir = base / "users" / f"usr_{uid}"
        if not user_dir.is_dir():
            user_dir = base / "users" / uid
        ssh_items = [s for s in legacy["ssh"] if s.get("user_id") == uid]
        ssh_json = user_dir / "ssh_connections.json"
        if not ssh_json.exists():
            ssh_json = user_dir / "ssh_connections.json.bak"
        if ssh_json.exists():
            try:
                with open(ssh_json, "r") as f:
                    ssh_items.extend(json.load(f))
            except Exception:
                pass
        for s in ssh_items:
            sid = s.get("id", secrets.token_hex(4))
            db.add(SshConnection(
                id=sid, user_id=uid,
                name=s.get("name", ""), host=s.get("host", ""),
                port=s.get("port", 22), username=s.get("username", ""),
                auth_type=s.get("authType", s.get("auth_type", "password")),
                password=s.get("password", ""),
                key_path=s.get("keyPath", s.get("key_path", "")),
                connection_mode=s.get("connection_mode", "direct"),
                agent_id=s.get("agent_id", ""),
                remark=s.get("remark", ""),
                created_at=s.get("createdAt", s.get("created_at", now_str)),
            ))

        # 链接: 从旧SQLite + 用户目录JSON → 迁移到 LinkGroup + LinkItem
        link_items_raw = [l for l in legacy["links"] if l.get("user_id") == uid]
        links_json = user_dir / "links.json"
        if not links_json.exists():
            links_json = user_dir / "links.json.bak"
        if links_json.exists():
            try:
                with open(links_json, "r") as f:
                    link_items_raw.append({"user_id": uid, "data": json.dumps(json.load(f), ensure_ascii=False)})
            except Exception:
                pass
        for l in link_items_raw:
            try:
                old_data = json.loads(l.get("data", '{"groups":[]}'))
                groups = old_data.get("groups", [])
                for g in groups:
                    gid = f"lg_{secrets.token_hex(4)}"
                    db.add(LinkGroup(id=gid, user_id=uid, name=g.get("name", "未分组"), created_at=now_str))
                    await db.flush()
                    for item in g.get("links", []):
                        db.add(LinkItem(
                            id=item.get("id", f"li_{secrets.token_hex(4)}"),
                            user_id=uid, group_id=gid,
                            title=item.get("title", ""),
                            url=item.get("url", ""),
                            description=item.get("description", ""),
                            created_at=now_str,
                        ))
            except Exception:
                pass

        # 每用户 SFTP 配置
        sftp_json = user_dir / "sftp_config.json"
        if not sftp_json.exists():
            sftp_json = user_dir / "sftp_config.json.bak"
        if sftp_json.exists():
            try:
                with open(sftp_json, "r") as f:
                    sftp_data = json.load(f)
                db.add(SftpConfig(
                    id=f"sftp_user_{uid}", scope="per_user", user_id=uid,
                    share_dir=sftp_data.get("share_dir", ""),
                    read_only=sftp_data.get("read_only", True),
                    username=sftp_data.get("username", "ppy"),
                    password=encrypt_secret(sftp_data.get("password", "changeme")),
                    port=sftp_data.get("port", 2222),
                    bind=sftp_data.get("bind", "0.0.0.0"),
                ))
            except Exception:
                pass

    # 3) 迁移 agents (旧SQLite + 保留注册)
    for a in legacy["agents"]:
        result = await db.execute(select(Agent).where(Agent.id == a["id"]))
        if result.scalar_one_or_none():
            continue
        db.add(Agent(
            id=a["id"], name=a.get("name", a["id"]),
            token=a.get("token", secrets.token_hex(32)),
            remark=a.get("remark", ""), is_active=a.get("is_active", True),
            created_at=a.get("created_at", now_str),
        ))

    # 4) 全局 SFTP 配置
    config_sftp = Path(os.environ.get("CONFIG_DIR", str(Path(__file__).resolve().parent.parent / "config"))) / "sftp_config.json"
    if config_sftp.exists():
        try:
            with open(config_sftp, "r") as f:
                sftp_data = json.load(f)
            db.add(SftpConfig(
                id="sftp_global", scope="global", user_id="",
                share_dir=sftp_data.get("share_dir", ""),
                read_only=sftp_data.get("read_only", True),
                username=sftp_data.get("username", "ppy"),
                password=encrypt_secret(sftp_data.get("password", "changeme")),
                port=sftp_data.get("port", 2222),
                bind=sftp_data.get("bind", "0.0.0.0"),
            ))
        except Exception:
            pass

    await db.commit()

    # 5) 备份 JSON 文件
    _backup_json_files(base, shared)


def _backup_json_files(base: Path, shared: Path):
    """Rename migrated JSON files to .bak (skip files already .bak)."""
    for f in [shared / "users.json"]:
        if f.exists():
            try:
                f.rename(f.with_suffix(".json.bak"))
            except Exception:
                pass

    users_dir = base / "users"
    if users_dir.is_dir():
        for user_dir in users_dir.iterdir():
            if user_dir.is_dir():
                for fname in ["ssh_connections.json", "links.json", "sftp_config.json"]:
                    fp = user_dir / fname
                    if fp.exists():
                        try:
                            fp.rename(user_dir / (fname + ".bak"))
                        except Exception:
                            pass

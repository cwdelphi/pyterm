"""
测试用同步数据库辅助函数 (MariaDB, 供 pytest 同步使用)

生产代码使用异步 SQLAlchemy; 测试大量依赖同步的
register_user / _read_users_file / _write_users_file 等旧接口,
此模块用同步 SQLAlchemy engine 提供等价实现。
"""
import os
from sqlalchemy import select, delete
from sqlalchemy.orm import sessionmaker
from sqlalchemy.ext.asyncio import create_async_engine

_DB_HOST = os.environ.get("DB_HOST", "mariadb")
_DB_PORT = int(os.environ.get("DB_PORT", "3306"))
_DB_USER = os.environ.get("DB_USER", "ppy")
_DB_PASS = os.environ.get("DB_PASS", "change_me_pass")
_DB_NAME = os.environ.get("DB_NAME", "ppy_tools")


def _make_sync_factories():
    from sqlalchemy import create_engine
    dsn = f"mysql+pymysql://{_DB_USER}:{_DB_PASS}@{_DB_HOST}:{_DB_PORT}/{_DB_NAME}?charset=utf8mb4"
    engine = create_engine(dsn, pool_pre_ping=True)
    return engine, sessionmaker(bind=engine, expire_on_commit=False)


def _read_users_file() -> dict:
    """返回 users.json 同构结构, 便于测试断言"""
    from app.database import User, LoginAttempt
    engine, Session = _make_sync_factories()
    with Session() as s:
        users = s.execute(select(User)).scalars().all()
        attempts = s.execute(select(LoginAttempt)).scalars().all()
    engine.dispose()
    return {
        "users": [
            {
                "id": u.id, "username": u.username, "password_hash": u.password_hash,
                "email": u.email, "role": u.role, "is_active": u.is_active,
                "created_at": u.created_at, "last_login": u.last_login,
            }
            for u in users
        ],
        "login_attempts": {a.username: {"count": a.count, "last_attempt": a.last_attempt} for a in attempts},
    }


def _write_users_file(data: dict):
    """写入 users 数据: 用户名匹配则更新, 否则插入; 测试前缀且名单外的用户删除"""
    from app.database import User
    keep = {u["username"] for u in data.get("users", [])}
    _test_prefixes = ("test_", "docs_api_", "iso_", "links_", "ssh_", "vnc_", "sftp_", "admin_")
    engine, Session = _make_sync_factories()
    with Session() as s:
        all_users = s.execute(select(User)).scalars().all()
        for u in all_users:
            if u.username not in keep and u.username.startswith(_test_prefixes):
                s.delete(u)
        for u in data.get("users", []):
            existing = s.execute(select(User).where(User.username == u["username"])).scalar_one_or_none()
            if existing:
                for k, v in u.items():
                    setattr(existing, k, v)
                existing.password_hash = u.get("password_hash", existing.password_hash)
            else:
                s.add(User(
                    id=u.get("id"), username=u["username"],
                    password_hash=u.get("password_hash", ""),
                    email=u.get("email", ""), role=u.get("role", "user"),
                    is_active=u.get("is_active", True),
                    created_at=u.get("created_at", ""),
                    last_login=u.get("last_login", ""),
                ))
        s.commit()
    engine.dispose()


def delete_user_by_username(username: str) -> bool:
    """按用户名删除用户（测试清理用）"""
    from app.database import User, SshConnection, Document
    engine, Session = _make_sync_factories()
    try:
        with Session() as s:
            u = s.execute(select(User).where(User.username == username)).scalar_one_or_none()
            if not u:
                engine.dispose()
                return False
            s.execute(delete(SshConnection).where(SshConnection.user_id == u.id))
            try:
                s.execute(delete(Document).where(Document.user_id == u.id))
            except Exception:
                pass
            s.delete(u)
            s.commit()
            engine.dispose()
            return True
    except Exception:
        engine.dispose()
        raise


def register_user(username: str, password: str, email: str = "", role: str = "user") -> dict:
    """同步注册用户"""
    import secrets
    from datetime import datetime, timezone
    from app.database import User
    from app.auth import _hash_password
    engine, Session = _make_sync_factories()
    with Session() as s:
        existing = s.execute(select(User).where(User.username == username)).scalar_one_or_none()
        if existing:
            uid = existing.id
            existing.role = role
            existing.email = email
        else:
            uid = f"usr_{secrets.token_hex(4)}"
            s.add(User(
                id=uid, username=username, password_hash=_hash_password(password),
                email=email, role=role, is_active=True,
                created_at=datetime.now(timezone.utc).isoformat(),
            ))
        s.commit()
    engine.dispose()
    return {"id": uid, "username": username, "email": email, "role": role}


def _get_user_id(username: str):
    """按用户名返回用户ID"""
    from app.database import User
    engine, Session = _make_sync_factories()
    with Session() as s:
        u = s.execute(select(User).where(User.username == username)).scalar_one_or_none()
    engine.dispose()
    return u.id if u else None


def _read_user_ssh(user_id: str) -> list[dict]:
    """同步读取用户SSH连接 (兼容旧接口) — 密码字段解密为明文"""
    from app.database import SshConnection
    from app.crypto import decrypt_secret
    engine, Session = _make_sync_factories()
    with Session() as s:
        rows = s.execute(select(SshConnection).where(SshConnection.user_id == user_id)).scalars().all()
        data = [
            {
                "id": r.id, "name": r.name, "host": r.host, "port": r.port,
                "username": r.username, "authType": r.auth_type,
                "password": decrypt_secret(r.password),
                "keyPath": r.key_path, "connection_mode": r.connection_mode,
                "agent_id": r.agent_id, "remark": r.remark, "createdAt": r.created_at,
                "connection_type": r.connection_type, "vnc_port": r.vnc_port,
                "vnc_password": decrypt_secret(r.vnc_password),
                "pixel_format": r.pixel_format,
                "color_depth": r.color_depth, "read_only": r.read_only,
            }
            for r in rows
        ]
    engine.dispose()
    return data


def _write_user_ssh(user_id: str, data: list[dict]):
    """同步写入用户SSH连接 (兼容旧接口, 全量替换) — 密码字段加密落库"""
    from app.database import SshConnection
    from app.crypto import encrypt_secret
    engine, Session = _make_sync_factories()
    with Session() as s:
        s.execute(delete(SshConnection).where(SshConnection.user_id == user_id))
        for item in data:
            s.add(SshConnection(
                id=item.get("id", ""), user_id=user_id,
                name=item.get("name", ""), host=item.get("host", ""),
                port=item.get("port", 22), username=item.get("username", ""),
                auth_type=item.get("authType", item.get("auth_type", "password")),
                password=encrypt_secret(item.get("password", "")),
                key_path=item.get("keyPath", item.get("key_path", "")),
                connection_mode=item.get("connection_mode", "agent"),
                agent_id=item.get("agent_id", ""),
                remark=item.get("remark", ""),
                created_at=item.get("createdAt", item.get("created_at", "")),
                connection_type=item.get("connection_type", "ssh"),
                vnc_port=item.get("vnc_port", 5900),
                vnc_password=encrypt_secret(item.get("vnc_password", "")),
                pixel_format=item.get("pixel_format", "tight"),
                color_depth=item.get("color_depth", "full"),
                read_only=item.get("read_only", False),
            ))
        s.commit()
    engine.dispose()


def ensure_agent(agent_id: str, token: str, name: str = "Test Agent"):
    """预置 Agent 记录(含 token), 供 webrtc 注册鉴权测试使用"""
    from datetime import datetime, timezone
    from app.database import Agent
    engine, Session = _make_sync_factories()
    with Session() as s:
        existing = s.execute(select(Agent).where(Agent.id == agent_id)).scalar_one_or_none()
        if existing:
            existing.token = token
            existing.name = name
            existing.is_active = True
        else:
            s.add(Agent(
                id=agent_id, name=name, token=token, is_active=True,
                created_at=datetime.now(timezone.utc).isoformat(),
            ))
        s.commit()
    engine.dispose()
    return {"id": agent_id, "token": token}


def ensure_gateway(gateway_id: str, token: str, name: str = "Test Gateway", url: str = "ws://localhost:5599"):
    """预置 Gateway 记录(含 token), 供 webrtc 注册鉴权测试使用"""
    from datetime import datetime, timezone
    from app.database import Gateway
    engine, Session = _make_sync_factories()
    with Session() as s:
        existing = s.execute(select(Gateway).where(Gateway.id == gateway_id)).scalar_one_or_none()
        if existing:
            existing.token = token
            existing.name = name
            existing.is_active = True
        else:
            s.add(Gateway(
                id=gateway_id, name=name, token=token, url=url, is_active=True,
                created_at=datetime.now(timezone.utc).isoformat(),
            ))
        s.commit()
    engine.dispose()
    return {"id": gateway_id, "token": token}


def delete_agent(agent_id: str):
    from app.database import Agent
    engine, Session = _make_sync_factories()
    with Session() as s:
        s.execute(delete(Agent).where(Agent.id == agent_id))
        s.commit()
    engine.dispose()


def delete_gateway(gateway_id: str):
    from app.database import Gateway
    engine, Session = _make_sync_factories()
    with Session() as s:
        s.execute(delete(Gateway).where(Gateway.id == gateway_id))
        s.commit()
    engine.dispose()
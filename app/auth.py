"""
认证模块 - 用户管理、JWT认证、密码哈希 (MariaDB + SQLAlchemy async)
"""
import os
import time
import socket
import threading
import logging
import secrets
import hmac
import base64
import hashlib
from datetime import datetime, timedelta, timezone
from typing import Optional

import jwt
import bcrypt
from fastapi import HTTPException, Depends, Request
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from sqlalchemy import select, update, func
from sqlalchemy.ext.asyncio import AsyncSession

from .database import get_db, _hash_password, User
from .i18n import t

# 配置
JWT_SECRET = os.environ.get("JWT_SECRET", "change_me_jwt")
JWT_ALGORITHM = "HS256"
JWT_EXPIRES_HOURS = int(os.environ.get("JWT_EXPIRES_HOURS", "24"))
BCRYPT_ROUNDS = 12
MAX_LOGIN_ATTEMPTS = 5
LOCKOUT_MINUTES = 15

# HTTP Bearer 认证
security = HTTPBearer(auto_error=False)


# ── 权限定义 ──────────────────────────────────────────────

PERMISSIONS = {
    "user:manage":   "用户管理",
    "agent:manage":  "Agent管理",
    "coturn:manage": "coturn管理",
    "audit:read":    "审计日志",
    "ssh:manage":    "远程管理",
    "doc:manage":    "文档管理",
    "link:manage":   "网址管理",
    "system:admin":  "系统管理",
}

ROLE_PERMISSIONS: dict[str, list[str]] = {
    "admin": list(PERMISSIONS.keys()),
    "user":  ["ssh:manage", "doc:manage", "link:manage", "agent:manage", "coturn:manage"],
}

ROLE_LABELS = {
    "admin": "管理员",
    "user": "普通用户",
}


def get_user_permissions(role: str) -> list[str]:
    return ROLE_PERMISSIONS.get(role, ROLE_PERMISSIONS["user"])


def has_permission(role: str, permission: str) -> bool:
    perms = get_user_permissions(role)
    return permission in perms or "system:admin" in perms


def _generate_token(user_id: str, username: str, role: str) -> str:
    payload = {
        "user_id": user_id,
        "username": username,
        "role": role,
        "exp": datetime.now(timezone.utc) + timedelta(hours=JWT_EXPIRES_HOURS),
        "iat": datetime.now(timezone.utc)
    }
    return jwt.encode(payload, JWT_SECRET, algorithm=JWT_ALGORITHM)


def _decode_token(token: str) -> dict:
    try:
        return jwt.decode(token, JWT_SECRET, algorithms=[JWT_ALGORITHM])
    except jwt.ExpiredSignatureError:
        raise HTTPException(status_code=401, detail=t("auth.token_expired"))
    except jwt.InvalidTokenError:
        raise HTTPException(status_code=401, detail=t("auth.invalid_token"))


async def _get_user_by_username(db: AsyncSession, username: str) -> Optional[User]:
    result = await db.execute(select(User).where(User.username == username))
    return result.scalar_one_or_none()


async def _get_user_by_id(db: AsyncSession, user_id: str) -> Optional[User]:
    result = await db.execute(select(User).where(User.id == user_id))
    return result.scalar_one_or_none()


def _verify_password(password: str, password_hash: str) -> bool:
    return bcrypt.checkpw(password.encode("utf-8"), password_hash.encode("utf-8"))


async def _check_login_attempts(db: AsyncSession, username: str) -> bool:
    from .database import LoginAttempt
    result = await db.execute(select(LoginAttempt).where(LoginAttempt.username == username))
    attempt = result.scalar_one_or_none()
    if not attempt:
        return True
    if attempt.count >= MAX_LOGIN_ATTEMPTS:
        lockout_until = attempt.last_attempt + (LOCKOUT_MINUTES * 60)
        if time.time() < lockout_until:
            return False
        attempt.count = 0
        await db.commit()
    return True


async def _record_login_attempt(db: AsyncSession, username: str, success: bool):
    from .database import LoginAttempt
    result = await db.execute(select(LoginAttempt).where(LoginAttempt.username == username))
    attempt = result.scalar_one_or_none()
    now_ts = int(time.time())
    if not attempt:
        db.add(LoginAttempt(username=username, count=0 if success else 1, last_attempt=now_ts))
    elif success:
        attempt.count = 0
    else:
        attempt.count += 1
        attempt.last_attempt = now_ts
    await db.commit()


async def register_user(db: AsyncSession, username: str, password: str, email: str, role: str = "user") -> dict:
    existing = await _get_user_by_username(db, username)
    if existing:
        raise HTTPException(status_code=400, detail=t("auth.username_exists"))
    if email:
        result = await db.execute(select(User).where(User.email == email, User.email != ""))
        if result.scalar_one_or_none():
            raise HTTPException(status_code=400, detail=t("auth.email_registered"))

    user_id = f"usr_{secrets.token_hex(4)}"
    db.add(User(
        id=user_id, username=username,
        password_hash=_hash_password(password),
        email=email, role=role, is_active=True,
        created_at=datetime.now(timezone.utc).isoformat(),
    ))
    await db.commit()
    return {"id": user_id, "username": username, "email": email, "role": role}


async def login_user(db: AsyncSession, username: str, password: str) -> dict:
    if not await _check_login_attempts(db, username):
        raise HTTPException(status_code=429, detail=t("auth.login_too_many", minutes=LOCKOUT_MINUTES))

    user = await _get_user_by_username(db, username)
    if not user:
        await _record_login_attempt(db, username, False)
        raise HTTPException(status_code=401, detail=t("auth.user_not_found"))

    if not user.is_active:
        raise HTTPException(status_code=403, detail=t("auth.account_disabled"))

    if not _verify_password(password, user.password_hash):
        await _record_login_attempt(db, username, False)
        raise HTTPException(status_code=401, detail=t("auth.user_not_found"))

    await _record_login_attempt(db, username, True)

    user.last_login = datetime.now(timezone.utc).isoformat()
    user.login_count += 1
    await db.commit()

    token = _generate_token(user.id, user.username, user.role)
    return {
        "token": token,
        "user": {
            "id": user.id,
            "username": user.username,
            "email": user.email,
            "role": user.role
        }
    }


async def get_current_user(request: Request, credentials: HTTPAuthorizationCredentials = Depends(security), db: AsyncSession = Depends(get_db)) -> dict:
    if not credentials:
        raise HTTPException(status_code=401, detail=t("auth.not_logged_in"))

    payload = _decode_token(credentials.credentials)
    user = await _get_user_by_id(db, payload["user_id"])
    if not user:
        raise HTTPException(status_code=401, detail=t("auth.user_not_exist"))
    if not user.is_active:
        raise HTTPException(status_code=403, detail=t("auth.account_disabled"))

    return {
        "id": user.id,
        "username": user.username,
        "email": user.email,
        "role": user.role
    }


def require_admin(user: dict = Depends(get_current_user)) -> dict:
    if user["role"] != "admin":
        raise HTTPException(status_code=403, detail=t("auth.admin_required"))
    return user


def require_permission(perm: str):
    """返回一个依赖函数，检查当前用户是否拥有指定权限"""
    def _check(user: dict = Depends(get_current_user)) -> dict:
        if not has_permission(user["role"], perm):
            raise HTTPException(status_code=403, detail=t("auth.permission_required", perm=PERMISSIONS.get(perm, perm)))
        return user
    return _check


# ── TURN/HMAC 凭证 ──────────────────────────────────────

def generate_turn_credentials(username: str, ttl: int = 604800, turn_secret: str = None) -> tuple[str, str]:
    if not turn_secret:
        turn_secret = os.environ.get("TURN_SECRET", "change_me_turn")
    timestamp = int(time.time()) + ttl
    temporary_username = f"{timestamp}:{username}"
    mac = hmac.new(turn_secret.encode(), temporary_username.encode(), hashlib.sha1)
    credential = base64.b64encode(mac.digest()).decode()
    return temporary_username, credential


# ── L1(延迟): STUN 可达性探测 + 结果缓存 ───────────────────────────
# 探不到的 STUN(如国内访问 stun.l.google.com)会让 ICE 候选收集/检查白等超时
# (实测单条约 3s)。探测在后台线程进行, 读缓存不阻塞请求；缓存未命中或过期
# 按"可达"处理(fail-open), 由后台线程每 60s 刷新。
STUN_PROBE_TTL = 300
STUN_PROBE_TIMEOUT = 0.8
STUN_PROBE_INTERVAL = 60

_stun_cache: dict = {}
_stun_lock = threading.Lock()
_stun_thread: Optional[threading.Thread] = None
_stun_ready = threading.Event()  # 首轮探测完成标记

_STUN_LOGGER = logging.getLogger("pyterm.stun")


def _stun_probe(host: str, port: int) -> bool:
    """发 20 字节 STUN Binding Request(RFC5389), 收到 ≥20 字节响应即视为可达"""
    try:
        infos = socket.getaddrinfo(host, port, 0, socket.SOCK_DGRAM)
        if not infos:
            return False
        family, socktype, proto, _, addr = infos[0]
        s = socket.socket(family, socktype, proto)
        s.settimeout(STUN_PROBE_TIMEOUT)
        try:
            s.sendto(b"\x00\x01\x00\x00" + b"\x21\x12\xa4\x42" + b"\x01" * 12, addr)
            data, _ = s.recvfrom(2048)
            # 00xxxxxx = Binding Success Response
            return len(data) >= 20 and (data[0] & 0xC0) == 0
        finally:
            s.close()
    except Exception as e:
        _STUN_LOGGER.debug("STUN probe fail %s:%s -> %s", host, port, e)
        return False


def _stun_reachable(host: str, port: int) -> bool:
    """读结果缓存；未探测/过期按可达处理(fail-open)"""
    with _stun_lock:
        hit = _stun_cache.get(f"{host}:{port}")
    if hit and time.time() - hit[1] <= STUN_PROBE_TTL:
        return bool(hit[0])
    return True


def _stun_probe_round() -> None:
    """并行探测全部目标(单轮耗时≈一个超时, 而非 N×超时)"""
    with _stun_lock:
        targets = list(_stun_targets)

    def _one(host: str, port: int) -> None:
        ok = _stun_probe(host, port)
        with _stun_lock:
            _stun_cache[f"{host}:{port}"] = (ok, time.time())
        _STUN_LOGGER.info("STUN probe %s:%s reachable=%s", host, port, ok)

    workers = [threading.Thread(target=_one, args=t, daemon=True) for t in targets]
    for w in workers:
        w.start()
    for w in workers:
        w.join()


def _stun_probe_loop():
    _stun_probe_round()
    _stun_ready.set()
    while True:
        time.sleep(STUN_PROBE_INTERVAL)
        _stun_probe_round()


_stun_targets: set = set()


def _ensure_stun_probe(targets) -> None:
    """惰性启动后台探测线程(进程内一次)"""
    global _stun_thread
    with _stun_lock:
        _stun_targets.update(targets)
        if _stun_thread and _stun_thread.is_alive():
            return
        _stun_thread = threading.Thread(target=_stun_probe_loop, daemon=True, name="stun-probe")
        _stun_thread.start()


def _stun_entries(server_ip: str, coturn_port: int, reachable) -> list[dict]:
    """按可达性组装 STUN 条目: 本机/已配置前置, 不可达的不下发(可单测)"""
    local = (server_ip, coturn_port)
    google = (("stun.l.google.com", 19302), ("stun1.l.google.com", 19302))
    entries = []
    if reachable(*local):
        entries.append({"urls": ["stun:" + server_ip + ":" + str(coturn_port)]})
    google_urls = [f"stun:{h}:{p}" for h, p in google if reachable(h, p)]
    if google_urls:
        entries.append({"urls": google_urls})
    if not entries:
        # 一个都探不通也不能把 STUN 清空 → 保留本机配置项
        entries.append({"urls": ["stun:" + server_ip + ":" + str(coturn_port)]})
    return entries


def get_ice_servers(user_id: str, coturn_host: str = None, coturn_port: int = 19302, coturn_tls_port: int = 5349, coturn_secret: str = None) -> list[dict]:
    server_ip = os.environ.get("SERVER_PUBLIC_IP", "localhost")
    _ensure_stun_probe([(server_ip, coturn_port),
                        ("stun.l.google.com", 19302), ("stun1.l.google.com", 19302)])
    # 首次调用最多等一轮探测: agent/网关只在注册时收一次 ICE 列表,
    # 拿到 fail-open 的旧列表会一直带着不可达的 STUN(阻塞上限≈1.4s, 仅一次)
    if not _stun_ready.is_set():
        _stun_ready.wait(STUN_PROBE_TIMEOUT + 0.6)
    # L1: 本机 STUN 前置, 探测不可达的不下发(避免客户端白等超时)
    ice_servers = _stun_entries(server_ip, coturn_port, _stun_reachable)
    if coturn_host:
        username, credential = generate_turn_credentials(user_id, turn_secret=coturn_secret)
        ice_servers.append({
            "urls": [
                f"turn:{coturn_host}:{coturn_port}?transport=udp",
                f"turn:{coturn_host}:{coturn_port}?transport=tcp",
                f"turns:{coturn_host}:{coturn_tls_port}?transport=tcp"
            ],
            "username": username,
            "credential": credential
        })
    return ice_servers


def get_user_data_dir(user_id: str):
    """获取用户数据目录(兼容旧接口)"""
    from pathlib import Path
    DATA_DIR = Path(os.environ.get("DATA_DIR", str(Path(__file__).resolve().parent.parent / "md"))).resolve()
    user_dir = DATA_DIR / "users" / f"usr_{user_id}"
    user_dir.mkdir(parents=True, exist_ok=True)
    return user_dir


async def log_audit(db, user_id: str, username: str, action: str,
                    target_type: str = "", target_id: str = "",
                    detail: str = "", ip: str = ""):
    """写入审计日志(失败不阻断主业务,如磁盘满/表满)"""
    from .database import AuditLog
    from datetime import datetime, timezone
    try:
        db.add(AuditLog(
            user_id=user_id, username=username, action=action,
            target_type=target_type, target_id=target_id,
            detail=detail, ip=ip,
            created_at=datetime.now(timezone.utc).isoformat(),
        ))
        await db.commit()
    except Exception as e:
        try:
            await db.rollback()
        except Exception:
            pass
        import logging
        logging.getLogger("pyterm.audit").warning("log_audit failed action=%s err=%s", action, e)


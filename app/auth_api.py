"""认证API路由 - MariaDB + SQLAlchemy async"""
from pydantic import BaseModel, Field
import os
import secrets
import time
import jwt
from datetime import datetime, timezone

from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import PlainTextResponse
from sqlalchemy import select, update, delete, func
from sqlalchemy.ext.asyncio import AsyncSession

from .database import get_db, _hash_password
from .models import (
    UserRegister, UserLogin, TokenResponse, UserResponse, PasswordChange,
    AgentAddReq, AgentUpdateReq,
    AdminUserCreate, AdminUserUpdate, AdminUserResetPassword,
    CoturnServerAdd, CoturnServerUpdate, AuditLogResponse,
    BatchDeleteRequest,
)
from .database import User, Agent, AuditLog, CoturnServer, Gateway
from .auth import (
    register_user, login_user, get_current_user, require_admin,
    require_permission, _get_user_by_id, _verify_password, get_ice_servers,
    log_audit, JWT_SECRET, JWT_ALGORITHM, _decode_token,
)
from .i18n import t

auth_router = APIRouter(prefix="/api/auth", tags=["认证"])
admin_router = APIRouter(prefix="/api/admin", tags=["管理"])
webrtc_router = APIRouter(prefix="/api/webrtc", tags=["WebRTC"])
deploy_router = APIRouter(prefix="/api/deploy", tags=["部署"])


# ════════════════════════════════════════════════════════════
#  认证 API
# ════════════════════════════════════════════════════════════

@auth_router.post("/register", response_model=UserResponse)
async def auth_register(req: UserRegister, request: Request, db: AsyncSession = Depends(get_db)):
    result = await register_user(db, req.username, req.password, req.email)
    ip = request.client.host if request.client else ""
    await log_audit(db, result["id"], req.username, "auth.register", "user", result["id"], req.username, ip)
    return result


@auth_router.post("/login", response_model=TokenResponse)
async def auth_login(req: UserLogin, request: Request, db: AsyncSession = Depends(get_db)):
    result = await login_user(db, req.username, req.password)
    ip = request.client.host if request.client else ""
    await log_audit(db, result["user"]["id"], req.username, "auth.login", "user", result["user"]["id"], req.username, ip)
    return result


@auth_router.post("/logout")
def auth_logout(user: dict = Depends(get_current_user)):
    return {"ok": True, "detail": t("auth.logged_out")}


@auth_router.get("/me", response_model=UserResponse)
def auth_me(user: dict = Depends(get_current_user)):
    return user


@auth_router.put("/password")
async def auth_change_password(req: PasswordChange, request: Request, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    db_user = await _get_user_by_id(db, user["id"])
    if not db_user:
        raise HTTPException(status_code=404, detail=t("auth.user_not_found_404"))
    if not _verify_password(req.old_password, db_user.password_hash):
        raise HTTPException(status_code=400, detail=t("auth.old_password_wrong"))
    db_user.password_hash = _hash_password(req.new_password)
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "password.change", "user", user["id"], "", ip)
    return {"ok": True, "detail": t("auth.password_updated")}


@auth_router.get("/verify")
async def auth_verify(token: str = "", request: Request = None, db: AsyncSession = Depends(get_db)):
    if not token:
        auth_header = request.headers.get("Authorization", "") if request else ""
        if auth_header.startswith("Bearer "):
            token = auth_header[7:]
    if not token:
        from fastapi.responses import JSONResponse
        return JSONResponse(status_code=401, content={"detail": t("auth.missing_token")})
    try:
        from .auth import _decode_token
        payload = _decode_token(token)
    except Exception:
        from fastapi.responses import JSONResponse
        return JSONResponse(status_code=401, content={"detail": t("auth.invalid_token_401")})
    user = await _get_user_by_id(db, payload.get("user_id", ""))
    if not user or not user.is_active:
        from fastapi.responses import JSONResponse
        return JSONResponse(status_code=401, content={"detail": t("auth.user_not_exist_or_disabled")})
    return {"user_id": user.id, "username": user.username, "role": user.role}


# ════════════════════════════════════════════════════════════
#  管理员用户管理 API
# ════════════════════════════════════════════════════════════

@admin_router.get("/users")
async def admin_list_users(user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(User).order_by(User.created_at.desc()))
    users = result.scalars().all()
    return {"users": [
        {
            "id": u.id, "username": u.username, "email": u.email,
            "role": u.role, "avatar": u.avatar, "phone": u.phone,
            "created_at": u.created_at, "last_login": u.last_login,
            "last_active": u.last_active, "login_count": u.login_count,
            "is_active": u.is_active,
        }
        for u in users
    ]}


@admin_router.post("/users")
async def admin_create_user(req: AdminUserCreate, request: Request, user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    existing = await db.execute(select(User).where(User.username == req.username))
    if existing.scalar_one_or_none():
        raise HTTPException(status_code=400, detail=t("auth.username_exists"))
    uid = f"usr_{secrets.token_hex(4)}"
    db.add(User(
        id=uid, username=req.username, password_hash=_hash_password(req.password),
        email=req.email, role=req.role, avatar=req.avatar, phone=req.phone,
        is_active=True, created_at=datetime.now(timezone.utc).isoformat(),
    ))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "user.create", "user", uid, req.username, ip)
    return {"ok": True, "id": uid}


@admin_router.put("/users/{user_id}")
async def admin_update_user(user_id: str, req: AdminUserUpdate, request: Request, user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    db_user = await _get_user_by_id(db, user_id)
    if not db_user:
        raise HTTPException(status_code=404, detail=t("auth.user_not_found_404"))
    if db_user.id == user["id"] and req.is_active is False:
        raise HTTPException(status_code=400, detail=t("admin.cannot_disable_self"))
    updates = {}
    if req.username is not None: updates["username"] = req.username
    if req.email is not None: updates["email"] = req.email
    if req.role is not None: updates["role"] = req.role
    if req.avatar is not None: updates["avatar"] = req.avatar
    if req.phone is not None: updates["phone"] = req.phone
    if req.is_active is not None: updates["is_active"] = req.is_active
    if updates:
        await db.execute(update(User).where(User.id == user_id).values(**updates))
        await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "user.update", "user", user_id, req.username or user_id, ip)
    return {"ok": True}


@admin_router.delete("/users/{user_id}")
async def admin_delete_user(user_id: str, request: Request, user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    if user["id"] == user_id:
        raise HTTPException(status_code=400, detail=t("admin.cannot_delete_self"))
    db_user = await _get_user_by_id(db, user_id)
    if not db_user:
        raise HTTPException(status_code=404, detail=t("auth.user_not_found_404"))
    if db_user.role == "admin":
        raise HTTPException(status_code=400, detail=t("admin.cannot_delete_admin"))
    await db.execute(delete(User).where(User.id == user_id))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "user.delete", "user", user_id, db_user.username, ip)
    return {"ok": True, "detail": t("admin.user_deleted")}


@admin_router.post("/users/{user_id}/password")
async def admin_reset_password(user_id: str, req: AdminUserResetPassword, request: Request, user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    db_user = await _get_user_by_id(db, user_id)
    if not db_user:
        raise HTTPException(status_code=404, detail=t("auth.user_not_found_404"))
    db_user.password_hash = _hash_password(req.new_password)
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "user.reset_password", "user", user_id, db_user.username, ip)
    return {"ok": True, "detail": t("admin.password_reset")}


@admin_router.put("/users/{user_id}/status")
async def admin_toggle_user_status(user_id: str, request: Request, user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    if user["id"] == user_id:
        raise HTTPException(status_code=400, detail=t("admin.cannot_modify_self"))
    db_user = await _get_user_by_id(db, user_id)
    if not db_user:
        raise HTTPException(status_code=404, detail=t("auth.user_not_found_404"))
    db_user.is_active = not db_user.is_active
    await db.commit()
    ip = request.client.host if request.client else ""
    action = "user.enable" if db_user.is_active else "user.disable"
    await log_audit(db, user["id"], user["username"], action, "user", user_id, db_user.username, ip)
    return {"ok": True, "is_active": db_user.is_active}


# ════════════════════════════════════════════════════════════
#  角色权限 API
# ════════════════════════════════════════════════════════════

@admin_router.get("/roles")
async def admin_get_roles(user: dict = Depends(require_admin)):
    from .auth import PERMISSIONS, ROLE_PERMISSIONS, ROLE_LABELS
    roles = []
    for role_key, perms in ROLE_PERMISSIONS.items():
        roles.append({
            "key": role_key,
            "label": ROLE_LABELS.get(role_key, role_key),
            "permissions": perms,
        })
    return {"roles": roles, "all_permissions": PERMISSIONS}


@admin_router.get("/me/permissions")
async def admin_my_permissions(user: dict = Depends(get_current_user)):
    from .auth import get_user_permissions, PERMISSIONS
    perms = get_user_permissions(user["role"])
    return {"role": user["role"], "permissions": perms, "all_permissions": PERMISSIONS}


# ════════════════════════════════════════════════════════════
#  审计日志 API
# ════════════════════════════════════════════════════════════

@admin_router.get("/audit")
async def admin_list_audit(user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(AuditLog).order_by(AuditLog.id.desc()).limit(200))
    logs = result.scalars().all()
    return {"logs": [
        {
            "id": l.id, "user_id": l.user_id, "username": l.username,
            "action": l.action, "target_type": l.target_type,
            "target_id": l.target_id, "detail": l.detail,
            "ip": l.ip, "created_at": l.created_at,
        }
        for l in logs
    ]}


# ════════════════════════════════════════════════════════════
#  WebRTC ICE servers
# ════════════════════════════════════════════════════════════

@webrtc_router.get("/ice-servers")
async def get_ice_servers_config(user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(CoturnServer).where(CoturnServer.is_active == True).order_by(CoturnServer.is_default.desc()).limit(1))
    coturn = result.scalar_one_or_none()
    if coturn:
        return {"ice_servers": get_ice_servers(user["id"], coturn.host, coturn.port, coturn.tls_port, coturn.secret)}
    return {"ice_servers": get_ice_servers(user["id"])}


class WebtermOpenReq(BaseModel):
    agent_id: str = Field("", description="Agent ID")
    agent_name: str = Field("", description="Agent 名称(审计展示)")


@webrtc_router.post("/webterm-open")
async def webterm_open(req: WebtermOpenReq, request: Request,
                       user: dict = Depends(require_permission("ssh:manage")),
                       db: AsyncSession = Depends(get_db)):
    """webterm 控制台打开审计(N6: 可见即有 shell 权限, 打开动作留痕)"""
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "agent.webterm_open",
                    "agent", req.agent_id, req.agent_name, ip)
    return {"ok": True}


# ════════════════════════════════════════════════════════════
#  Agent CRUD (admin)
# ════════════════════════════════════════════════════════════

@admin_router.get("/agent-id/next")
async def admin_next_agent_id(user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    today = datetime.now(timezone.utc).strftime("%Y%m%d")
    prefix = f"wragent-{today}"
    result = await db.execute(
        select(Agent.id).where(Agent.id.like(f"{prefix}%")).order_by(Agent.id.desc())
    )
    existing_ids = [row[0] for row in result.all()]
    seq = 1
    for eid in existing_ids:
        try:
            num = int(eid.split(today)[1])
            if num >= seq:
                seq = num + 1
        except (IndexError, ValueError):
            pass
    return {"id": f"{prefix}{seq:02d}"}


@admin_router.get("/agents")
async def admin_list_agents(user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    uid = user["id"]
    # 查询: 自己的 + 所有人共享的 + 指定共享给自己的
    result = await db.execute(select(Agent).where(
        (Agent.owner_id == uid) |
        (Agent.shared_with == "all") |
        (Agent.shared_with.contains(f'"{uid}"'))
    ).order_by(Agent.created_at.desc()))
    agents = result.scalars().all()
    # 获取所有相关用户名（用于显示来源）
    owner_ids = list(set(a.owner_id for a in agents if a.owner_id))
    user_map = {}
    if owner_ids:
        ur = await db.execute(select(User.id, User.username).where(User.id.in_(owner_ids)))
        user_map = {row[0]: row[1] for row in ur.all()}
    # 合并在线状态
    from .api_isolated import _online_agents, _check_version_upgrade
    agent_list = []
    for a in agents:
        online_info = _online_agents.get(a.id, {})
        owner_name = user_map.get(a.owner_id, "")
        is_owner = a.owner_id == uid
        _online_ver = online_info.get("version") or ""
        _ver = a.version if _online_ver in ("", "unknown") else _online_ver
        _needs_upgrade, _latest_version = _check_version_upgrade("agent", _ver)
        agent_list.append({
            "id": a.id, "name": a.name, "token": a.token,
            "coturn_id": a.coturn_id or "",
            "remark": a.remark, "is_active": a.is_active,
            "ip": online_info.get("ip", ""),
            "online": bool(online_info),
            "conn_type": online_info.get("conn_type") or a.conn_type or "",
            "owner_id": a.owner_id, "owner_name": owner_name if not is_owner else "",
            "is_owner": is_owner, "shared_with": a.shared_with,
            "version": _ver,
            "needs_upgrade": _needs_upgrade,
            "latest_version": _latest_version,
        })
    return {"agents": agent_list}


@admin_router.post("/agents/{agent_id}/upgrade")
async def upgrade_agent(agent_id: str, user: dict = Depends(require_permission("agent:manage"))):
    """发送upgrade指令到在线agent"""
    import os
    from .api_isolated import _online_agents, push_upgrade_to_agent
    latest = os.environ.get("LATEST_AGENT_VERSION", "")
    if not latest:
        from fastapi import HTTPException
        raise HTTPException(status_code=400, detail=t("admin.no_version_configured"))
    if agent_id not in _online_agents:
        from fastapi import HTTPException
        raise HTTPException(status_code=400, detail=t("admin.agent_offline"))
    base_url = os.environ.get("PUBLIC_URL", "https://domain:5588")
    download_url = f"{base_url}/api/deploy/wragent/linux-amd64"
    ok = await push_upgrade_to_agent(agent_id, latest, download_url)
    if not ok:
        from fastapi import HTTPException
        raise HTTPException(status_code=500, detail=t("admin.send_upgrade_failed"))
    return {"ok": True, "message": t("admin.upgrade_sent", agent_id=agent_id, version=latest)}


@admin_router.post("/gateways/{gateway_id}/upgrade")
async def upgrade_gateway(gateway_id: str, user: dict = Depends(require_permission("agent:manage"))):
    """发送upgrade指令到在线gateway"""
    import os
    from .api_isolated import _online_gateways, push_upgrade_to_gateway
    latest = os.environ.get("LATEST_GATEWAY_VERSION", "")
    if not latest:
        from fastapi import HTTPException
        raise HTTPException(status_code=400, detail=t("admin.no_version_configured"))
    if gateway_id not in _online_gateways:
        from fastapi import HTTPException
        raise HTTPException(status_code=400, detail=t("admin.gateway_offline"))
    base_url = os.environ.get("PUBLIC_URL", "https://domain:5588")
    download_url = f"{base_url}/api/deploy/wrgateway/linux-amd64"
    ok = await push_upgrade_to_gateway(gateway_id, latest, download_url)
    if not ok:
        from fastapi import HTTPException
        raise HTTPException(status_code=500, detail=t("admin.send_upgrade_failed"))
    return {"ok": True, "message": t("admin.upgrade_sent", agent_id=gateway_id, version=latest)}


@admin_router.post("/agents")
async def admin_add_agent(req: AgentAddReq, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    existing = await db.execute(select(Agent).where(Agent.id == req.id))
    if existing.scalar_one_or_none():
        raise HTTPException(status_code=400, detail="Agent ID already exists")
    token = secrets.token_hex(32)
    db.add(Agent(
        id=req.id, name=req.name, token=token,
        coturn_id=req.coturn_id or None,
        remark=req.remark, is_active=True,
        created_at=datetime.now(timezone.utc).isoformat(),
        owner_id=user["id"],
    ))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "agent.create", "agent", req.id, req.name, ip)
    return {"id": req.id, "token": token, "name": req.name}


@admin_router.put("/agents/{agent_id}")
async def admin_update_agent(agent_id: str, req: AgentUpdateReq, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    if agent.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_edit_others_agent"))
    if req.name is not None: agent.name = req.name
    if req.remark is not None: agent.remark = req.remark
    if req.coturn_id is not None: agent.coturn_id = req.coturn_id or None
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "agent.update", "agent", agent_id, agent.name, ip)
    return {"ok": True}


@admin_router.delete("/agents/{agent_id}")
async def admin_delete_agent(agent_id: str, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    if agent.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_delete_others_agent"))
    await db.execute(delete(Agent).where(Agent.id == agent_id))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "agent.delete", "agent", agent_id, agent.name, ip)
    return {"ok": True}


@admin_router.post("/agents/{agent_id}/token")
async def admin_regenerate_token(agent_id: str, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    if agent.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_agent"))
    new_token = secrets.token_hex(32)
    agent.token = new_token
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "agent.token_regenerate", "agent", agent_id, agent.name, ip)
    return {"token": new_token}


@admin_router.post("/agents/{agent_id}/status")
async def admin_toggle_agent_status(agent_id: str, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    if agent.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_agent"))
    agent.is_active = not agent.is_active
    await db.commit()
    ip = request.client.host if request.client else ""
    action = "agent.enable" if agent.is_active else "agent.disable"
    await log_audit(db, user["id"], user["username"], action, "agent", agent_id, agent.name, ip)
    return {"ok": True, "is_active": agent.is_active}


@admin_router.post("/agents/{agent_id}/share")
async def admin_share_agent(agent_id: str, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db), body: dict = None):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    if agent.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_agent"))
    import json
    body = body or {}
    shared_with = body.get("shared_with", "private")
    if isinstance(shared_with, list):
        agent.shared_with = json.dumps(shared_with)
    else:
        agent.shared_with = str(shared_with)
    await db.commit()
    return {"ok": True, "shared_with": agent.shared_with}


@admin_router.get("/agents/{agent_id}/shares")
async def admin_get_agent_shares(agent_id: str, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    import json
    shared_with = agent.shared_with
    usernames = []
    if shared_with and shared_with not in ("private", "all"):
        try:
            uid_list = json.loads(shared_with)
            if uid_list:
                ur = await db.execute(select(User.id, User.username).where(User.id.in_(uid_list)))
                usernames = [{"id": row[0], "username": row[1]} for row in ur.all()]
        except Exception:
            pass
    return {"shared_with": shared_with, "users": usernames}


# ── Agent 配置管理 API ──────────────────────────────────────

import json as _json

class AgentConfigReq(BaseModel):
    ws_reconnect_interval: int = Field(5, ge=1, le=60, description="WebSocket重连间隔(秒)")
    ws_heartbeat_interval: int = Field(30, ge=5, le=120, description="心跳间隔(秒)")
    ice_cooldown: int = Field(2, ge=0, le=30, description="ICE冷却时间(秒)")
    log_level: str = Field("info", description="日志级别(debug/info/warn/error)")
    tunnels: list = Field(default=[], description="[兼容]旧版隧道列表(plugins为空时按protocol拆分)")
    plugins: dict = Field(default_factory=dict, description="插件配置字典 {tunnel,socks5}")


_PLUGIN_KEYS = {"tunnel", "socks5"}


def _strip_ssh_plugin(config: dict) -> dict:
    """存量 plugins.ssh 键剥离(内嵌SSH服务已移除, 仅 webterm 本地控制台, 兼容旧数据回写)"""
    plugins = config.get("plugins")
    if isinstance(plugins, dict) and "ssh" in plugins:
        out = dict(config)
        out["plugins"] = {k: v for k, v in plugins.items() if k != "ssh"}
        return out
    return config


def _validate_plugins(plugins: dict):
    """插件字典校验: 键白名单 + 逐插件schema"""
    if not plugins:
        return
    unknown = set(plugins.keys()) - _PLUGIN_KEYS
    if unknown:
        raise HTTPException(status_code=400, detail=f"未知插件配置: {sorted(unknown)}")
    for key, want_proto in (("tunnel", ("tcp", "udp")), ("socks5", ("socks5",))):
        if key not in plugins:
            continue
        bucket = plugins[key]
        if not isinstance(bucket, dict):
            raise HTTPException(status_code=400, detail=f"plugins.{key} 必须是对象")
        tl = bucket.get("tunnels") or []
        if not isinstance(tl, list):
            raise HTTPException(status_code=400, detail=f"plugins.{key}.tunnels 必须是列表")
        for t in tl:
            proto = t.get("protocol") or want_proto[0]
            if proto not in want_proto:
                raise HTTPException(status_code=400,
                                    detail=f"plugins.{key} 不允许 protocol={proto}")
            try:
                lp = int(t.get("local_port") or 0)
            except (TypeError, ValueError):
                raise HTTPException(status_code=400, detail="local_port 必须是整数")
            if not (1 <= lp <= 65535):
                raise HTTPException(status_code=400, detail=f"local_port 超出范围: {lp}")


def _migrate_agent_config(config: dict) -> dict:
    """旧 config_json(顶层tunnels) → plugins 结构(仅返回视图, 下次PUT落库)"""
    if "plugins" not in config:
        t_b, s_b = [], []
        for t in (config.get("tunnels") or []):
            (s_b if isinstance(t, dict) and t.get("protocol") == "socks5" else t_b).append(t)
        out = dict(config)
        out["plugins"] = {"tunnel": {"tunnels": t_b}, "socks5": {"tunnels": s_b}}
        config = out
    return _strip_ssh_plugin(config)


def _split_tunnels_to_plugins(tunnels: list) -> dict:
    """旧入参 tunnels → plugins 字典(兼容旧客户端PUT)"""
    t_b, s_b = [], []
    for t in (tunnels or []):
        (s_b if isinstance(t, dict) and t.get("protocol") == "socks5" else t_b).append(t)
    return {"tunnel": {"tunnels": t_b}, "socks5": {"tunnels": s_b}}


@admin_router.get("/agents/{agent_id}/config")
async def admin_get_agent_config(agent_id: str, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    config = {}
    if agent.config_json:
        try:
            config = _json.loads(agent.config_json)
        except Exception:
            config = {}
    # 旧格式懒迁移为 plugins 视图(不落库, 下次PUT转正)
    return {"config": _migrate_agent_config(config)}


@admin_router.put("/agents/{agent_id}/config")
async def admin_update_agent_config(agent_id: str, req: AgentConfigReq, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent:
        raise HTTPException(status_code=404, detail="Agent not found")
    if agent.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_agent"))
    plugins = req.plugins
    if not plugins and req.tunnels:
        # 兼容旧客户端: 仅传 tunnels → 拆分
        plugins = _split_tunnels_to_plugins(req.tunnels)
    # 存量 plugins.ssh 键接受并剥离(内嵌SSH服务已移除), 不报错并随落库回写清除
    plugins = _strip_ssh_plugin({"plugins": plugins})["plugins"]
    _validate_plugins(plugins)
    config = {
        "ws_reconnect_interval": req.ws_reconnect_interval,
        "ws_heartbeat_interval": req.ws_heartbeat_interval,
        "ice_cooldown": req.ice_cooldown,
        "log_level": req.log_level,
        "plugins": plugins,
    }
    agent.config_json = _json.dumps(config, ensure_ascii=False)
    await db.commit()
    # 推送配置到 Agent(_push_config_to_agent 自动附加 tunnels 镜像, 旧Agent兼容)
    from .api_isolated import _online_agents, _push_config_to_agent
    pushed = await _push_config_to_agent(agent_id, config)
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "agent.config_update", "agent", agent_id, agent.name, ip)
    return {"ok": True, "pushed": pushed}


# ════════════════════════════════════════════════════════════
#  用户批量删除 API
# ════════════════════════════════════════════════════════════

@admin_router.post("/users/batch-delete")
async def admin_batch_delete_users(req: BatchDeleteRequest, request: Request, user: dict = Depends(require_admin), db: AsyncSession = Depends(get_db)):
    deleted = []
    skipped = []
    for uid in req.ids:
        if uid == user["id"]:
            skipped.append({"id": uid, "reason": t("admin.cannot_delete_self")})
            continue
        target = await _get_user_by_id(db, uid)
        if target and target.role == "admin":
            skipped.append({"id": uid, "reason": t("admin.cannot_delete_admin")})
            continue
        if target:
            await db.execute(delete(User).where(User.id == uid))
            deleted.append(uid)
        else:
            skipped.append({"id": uid, "reason": t("auth.user_not_found_404")})
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "user.batch_delete", "user", ",".join(deleted), f"deleted={len(deleted)}", ip)
    return {"ok": True, "deleted": deleted, "skipped": skipped}


# ════════════════════════════════════════════════════════════
#  coturn 管理 API
# ════════════════════════════════════════════════════════════

@admin_router.get("/coturn")
async def admin_list_coturn(user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    uid = user["id"]
    result = await db.execute(select(CoturnServer).where(
        (CoturnServer.owner_id == uid) |
        (CoturnServer.shared_with == "all") |
        (CoturnServer.shared_with.contains(f'"{uid}"'))
    ).order_by(CoturnServer.created_at.desc()))
    servers = result.scalars().all()
    # 获取用户名
    owner_ids = list(set(c.owner_id for c in servers if c.owner_id))
    user_map = {}
    if owner_ids:
        ur = await db.execute(select(User.id, User.username).where(User.id.in_(owner_ids)))
        user_map = {row[0]: row[1] for row in ur.all()}
    return {"servers": [
        {
            "id": c.id, "name": c.name, "host": c.host,
            "port": c.port, "tls_port": c.tls_port, "secret": c.secret,
            "realm": c.realm, "relay_range": c.relay_range,
            "total_quota": c.total_quota, "is_active": c.is_active,
            "is_default": c.is_default,
            "remark": c.remark, "created_at": c.created_at,
            "health_status": c.health_status,
            "owner_id": c.owner_id, "owner_name": user_map.get(c.owner_id, "") if c.owner_id != uid else "",
            "is_owner": c.owner_id == uid, "shared_with": c.shared_with,
        }
        for c in servers
    ]}


@admin_router.post("/coturn")
async def admin_add_coturn(req: CoturnServerAdd, request: Request, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    coturn_id = f"coturn_{secrets.token_hex(4)}"
    db.add(CoturnServer(
        id=coturn_id, name=req.name, host=req.host,
        port=req.port, tls_port=req.tls_port, secret=req.secret,
        realm=req.realm, relay_range=req.relay_range,
        total_quota=req.total_quota,
        remark=req.remark, is_active=True, is_default=False,
        created_at=datetime.now(timezone.utc).isoformat(),
        owner_id=user["id"],
    ))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "coturn.create", "coturn", coturn_id, req.name, ip)
    return {"ok": True, "id": coturn_id}


@admin_router.put("/coturn/{coturn_id}")
async def admin_update_coturn(coturn_id: str, req: CoturnServerUpdate, request: Request, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(CoturnServer).where(CoturnServer.id == coturn_id))
    c = result.scalar_one_or_none()
    if not c:
        raise HTTPException(status_code=404, detail="coturn server not found")
    if c.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_edit_others_coturn"))
    updates = {}
    for field in ["name", "host", "port", "tls_port", "secret", "realm", "relay_range", "total_quota", "remark", "is_active"]:
        val = getattr(req, field, None)
        if val is not None:
            updates[field] = val
    if updates:
        await db.execute(update(CoturnServer).where(CoturnServer.id == coturn_id).values(**updates))
        await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "coturn.update", "coturn", coturn_id, c.name, ip)
    return {"ok": True}


@admin_router.delete("/coturn/{coturn_id}")
async def admin_delete_coturn(coturn_id: str, request: Request, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(CoturnServer).where(CoturnServer.id == coturn_id))
    c = result.scalar_one_or_none()
    if not c:
        raise HTTPException(status_code=404, detail="coturn server not found")
    if c.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_delete_others_coturn"))
    await db.execute(delete(CoturnServer).where(CoturnServer.id == coturn_id))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "coturn.delete", "coturn", coturn_id, c.name, ip)
    return {"ok": True}


@admin_router.post("/coturn/{coturn_id}/default")
async def admin_set_default_coturn(coturn_id: str, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    # 清除所有默认
    await db.execute(update(CoturnServer).values(is_default=False))
    # 设置新的默认
    result = await db.execute(select(CoturnServer).where(CoturnServer.id == coturn_id))
    c = result.scalar_one_or_none()
    if not c:
        raise HTTPException(status_code=404, detail="coturn server not found")
    c.is_default = True
    await db.commit()
    return {"ok": True}


@admin_router.post("/coturn/{coturn_id}/share")
async def admin_share_coturn(coturn_id: str, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db), body: dict = None):
    result = await db.execute(select(CoturnServer).where(CoturnServer.id == coturn_id))
    c = result.scalar_one_or_none()
    if not c:
        raise HTTPException(status_code=404, detail="coturn not found")
    if c.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_coturn"))
    import json
    body = body or {}
    shared_with = body.get("shared_with", "private")
    if isinstance(shared_with, list):
        c.shared_with = json.dumps(shared_with)
    else:
        c.shared_with = str(shared_with)
    await db.commit()
    return {"ok": True, "shared_with": c.shared_with}


@admin_router.get("/coturn/{coturn_id}/shares")
async def admin_get_coturn_shares(coturn_id: str, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(CoturnServer).where(CoturnServer.id == coturn_id))
    c = result.scalar_one_or_none()
    if not c:
        raise HTTPException(status_code=404, detail="coturn not found")
    import json
    shared_with = c.shared_with
    usernames = []
    if shared_with and shared_with not in ("private", "all"):
        try:
            uid_list = json.loads(shared_with)
            if uid_list:
                ur = await db.execute(select(User.id, User.username).where(User.id.in_(uid_list)))
                usernames = [{"id": row[0], "username": row[1]} for row in ur.all()]
        except Exception:
            pass
    return {"shared_with": shared_with, "users": usernames}


# ════════════════════════════════════════════════════════════
#  STUN/TURN 测试凭证
# ════════════════════════════════════════════════════════════

@admin_router.get("/coturn/{coturn_id}/test-credentials")
async def admin_get_test_credentials(coturn_id: str, user: dict = Depends(require_permission("coturn:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(CoturnServer).where(CoturnServer.id == coturn_id))
    c = result.scalar_one_or_none()
    if not c:
        raise HTTPException(status_code=404, detail="coturn not found")
    from .auth import generate_turn_credentials
    username, credential = generate_turn_credentials("test", turn_secret=c.secret)
    return {
        "username": username, "credential": credential,
        "host": c.host, "port": c.port, "tls_port": c.tls_port,
    }


# ════════════════════════════════════════════════════════════
#  Deploy Script Generation
# ════════════════════════════════════════════════════════════

def _resolve_public_base(request=None):
    """推导门户对外 base_url（部署脚本下载 / setup 链接使用）。
    优先级: env BASE_URL/PUBLIC_URL > 请求头(X-Forwarded-Proto/Host) > 默认。
    """
    base = os.environ.get("BASE_URL") or os.environ.get("PUBLIC_URL") or ""
    if not base and request is not None:
        proto = ""
        host = ""
        if hasattr(request, "headers"):
            proto = request.headers.get("x-forwarded-proto", "") or ""
            host = (request.headers.get("x-forwarded-host", "")
                    or request.headers.get("host", "") or "")
        if proto and host:
            base = f"{proto}://{host}"
    if not base:
        base = "https://domain:5588"
    base = base.rstrip("/")
    if "://" not in base:
        base = f"https://{base}"
    return base


def _get_deploy_config(request=None):
    base_url = _resolve_public_base(request)
    scheme, _, host = base_url.partition("://")
    if ":" not in host:
        default_port = "443" if scheme == "https" else "80"
        host = f"{host}:{default_port}"
        base_url = f"{scheme}://{host}"
    ws_scheme = "wss" if scheme == "https" else "ws"
    return {
        "base_url": base_url,
        "ws_url": f"{ws_scheme}://{host}/api/ws/webrtc",
        "server_url": f"{ws_scheme}://{host}/api/ws/webrtc",
    }


def _generate_docker_script(agent_id: str, config: dict, token: str = "") -> str:
    auth_token_line = f'"{token}"' if token else '""'
    return f'''#!/bin/bash
set -e
AGENT_ID="{agent_id}"
SERVER_URL="{config['server_url']}"
BASE_URL="{config['base_url']}"

# 非 root 时自动使用 sudo（兼容 curl | bash 管道执行）
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || {{ echo "需要 root 或 sudo 权限"; exit 1; }}
  SUDO="sudo"
else
  SUDO=""
fi

command -v docker >/dev/null 2>&1 || {{ echo "未安装 Docker"; exit 1; }}

echo "Docker deploy Agent: $AGENT_ID"

$SUDO mkdir -p /opt/wragent/config
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

BIN_URL="$BASE_URL/api/deploy/wragent/linux-$ARCH"
echo "下载 wragent: $BIN_URL"
BIN_TMP=/opt/wragent/.wragent.dl
curl -fsSL "$BIN_URL" -o "$BIN_TMP"
chmod +x "$BIN_TMP"
"$BIN_TMP" -v || {{ echo "下载的二进制无法执行"; exit 1; }}
$SUDO mv "$BIN_TMP" /opt/wragent/wragent
$SUDO chmod +x /opt/wragent/wragent

# 单实例: 清理本机已有 docker 版 wragent
for c in $($SUDO docker ps -aq --filter "name=wragent" 2>/dev/null || true); do
  $SUDO docker rm -f "$c" 2>/dev/null || true
done

$SUDO tee /opt/wragent/config/config.json > /dev/null << AGENTEOF
{{
  "server_url": "$SERVER_URL",
  "agent_id": "$AGENT_ID",
  "auth_token": {auth_token_line}
}}
AGENTEOF

# 极简运行时镜像（Go 静态编译，alpine 自带 sh/cp/chmod 与 CA 证书）
$SUDO docker run -d \
  --name wragent \
  --network host \
  --restart unless-stopped \
  -v /opt/wragent/config:/config:rw \
  -v /opt/wragent/wragent:/src/wragent:ro \
  alpine:3.20 \
  sh -c "cp /src/wragent /usr/local/bin/wragent && chmod +x /usr/local/bin/wragent && exec /usr/local/bin/wragent -config /config/config.json"

sleep 3
echo ""
echo "=== Agent $AGENT_ID 已启动 (Docker) ==="
$SUDO docker ps --filter "name=wragent" --format "{{{{.Names}}}} {{{{.Status}}}}" || true
echo "提示: 有 token 将直接注册上线；无 token 请到门户「系统管理 → Agent 管理 → 待注册 Agent」审批"
$SUDO docker logs wragent --tail 30 2>&1 | grep -iE "visit|authenticate|setup|认证|agent/s/|注册成功" | tail -5 || true
echo "日志: $SUDO docker logs wragent -f"
'''


def _generate_systemd_script(agent_id: str, config: dict, token: str = "") -> str:
    auth_token_line = f'"{token}"' if token else '""'
    return f'''#!/bin/bash
set -e
AGENT_ID="{agent_id}"
SERVER_URL="{config['server_url']}"
BASE_URL="{config['base_url']}"

# 非 root 时自动使用 sudo（兼容 curl | bash 管道执行）
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || {{ echo "需要 root 或 sudo 权限"; exit 1; }}
  SUDO="sudo"
else
  SUDO=""
fi

echo "systemd deploy Agent: $AGENT_ID"

$SUDO mkdir -p /opt/wragent/config
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac

BIN_URL="$BASE_URL/api/deploy/wragent/linux-$ARCH"
echo "下载 wragent: $BIN_URL"
BIN_TMP=/opt/wragent/.wragent.dl
curl -fsSL "$BIN_URL" -o "$BIN_TMP"
chmod +x "$BIN_TMP"
"$BIN_TMP" -v || {{ echo "下载的二进制无法执行"; exit 1; }}
$SUDO mv "$BIN_TMP" /opt/wragent/wragent
$SUDO chmod +x /opt/wragent/wragent

# 单实例: 清理本机已有 systemd 版 wragent
$SUDO systemctl stop wragent.service 2>/dev/null || true
$SUDO systemctl disable wragent.service 2>/dev/null || true
for u in $($SUDO systemctl list-unit-files 'wragent-*.service' --no-legend 2>/dev/null | awk '{{print $1}}'); do
  $SUDO systemctl stop "$u" 2>/dev/null || true
  $SUDO systemctl disable "$u" 2>/dev/null || true
done
$SUDO rm -f /etc/systemd/system/wragent.service /etc/systemd/system/wragent-*.service
$SUDO systemctl daemon-reload || true

$SUDO tee /opt/wragent/config/config.json > /dev/null << AGENTEOF
{{
  "server_url": "$SERVER_URL",
  "agent_id": "$AGENT_ID",
  "auth_token": {auth_token_line}
}}
AGENTEOF

$SUDO tee /etc/systemd/system/wragent.service > /dev/null << SVCEOF
[Unit]
Description=WRAgent
After=network.target
[Service]
Type=simple
ExecStart=/opt/wragent/wragent -config /opt/wragent/config/config.json
Restart=always
RestartSec=5
Environment=TZ=Asia/Shanghai
[Install]
WantedBy=multi-user.target
SVCEOF

$SUDO systemctl daemon-reload
$SUDO systemctl enable wragent
$SUDO systemctl restart wragent
sleep 3
echo ""
echo "=== Agent $AGENT_ID 已启动 (systemd) ==="
$SUDO systemctl status wragent --no-pager | head -6 || true
echo "提示: 有 token 将直接注册上线；无 token 请到门户「系统管理 → Agent 管理 → 待注册 Agent」审批"
$SUDO journalctl -u wragent -n 40 --no-pager 2>/dev/null | grep -iE "visit|authenticate|setup|认证|agent/s/|注册成功" | tail -5 || true
echo "日志: $SUDO journalctl -u wragent -f"
'''


def _generate_install_script(config: dict, method: str = "") -> str:
    """模式一：公开安装脚本（Tailscale 式）。
    - 无账户、无预建 Agent；agent_id 留空由 wragent 自动生成
    - 支持 ?method=systemd|docker 显式指定，否则 /dev/tty 交互菜单，非交互默认 systemd
    - 幂等：保留已有 config.json（identity 复用），单实例
    """
    method_line = f'METHOD="{method}"' if method else 'METHOD=""'
    return f'''#!/bin/bash
set -e
SERVER_URL="{config['server_url']}"
BASE_URL="{config['base_url']}"
{method_line}

# 部署方式优先级: ?method 参数 > WRAGENT_METHOD 环境变量 > 交互菜单 > 默认 systemd
if [ -n "${{1:-}}" ] && [ -z "$METHOD" ]; then METHOD="$1"; fi
if [ -n "${{WRAGENT_METHOD:-}}" ]; then METHOD="$WRAGENT_METHOD"; fi
if [ -z "$METHOD" ]; then
  if [ -e /dev/tty ]; then
    echo ""
    echo "=== Agent 安装 - 选择部署方式 ==="
    echo "  1) systemd（推荐，系统服务常驻）"
    echo "  2) docker（容器方式，需已装 Docker）"
    printf "请输入 1 或 2 后回车: "
    read -r _c < /dev/tty || true
    case "$_c" in
      2) METHOD="docker" ;;
      *) METHOD="systemd" ;;
    esac
  else
    METHOD="systemd"
    echo "非交互环境，默认 systemd（可用 ?method=docker 或 WRAGENT_METHOD=docker 显式指定）"
  fi
fi

# 非 root 自动使用 sudo
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || {{ echo "需要 root 或 sudo 权限"; exit 1; }}
  SUDO="sudo"
else
  SUDO=""
fi

$SUDO mkdir -p /opt/wragent/config
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "不支持的架构: $ARCH"; exit 1 ;;
esac
BIN_URL="$BASE_URL/api/deploy/wragent/linux-$ARCH"
echo "下载 wragent: $BIN_URL"
BIN_TMP=/opt/wragent/.wragent.dl
curl -fsSL "$BIN_URL" -o "$BIN_TMP"
chmod +x "$BIN_TMP"
"$BIN_TMP" -v || {{ echo "下载的二进制无法执行"; exit 1; }}
$SUDO mv "$BIN_TMP" /opt/wragent/wragent
$SUDO chmod +x /opt/wragent/wragent

# 单实例: 仅清理与本次方式同类的旧实例
if [ "$METHOD" = "docker" ]; then
  command -v docker >/dev/null 2>&1 || {{ echo "未安装 Docker"; exit 1; }}
  for c in $($SUDO docker ps -aq --filter "name=wragent" 2>/dev/null || true); do
    $SUDO docker rm -f "$c" 2>/dev/null || true
  done
else
  command -v systemctl >/dev/null 2>&1 || {{ echo "未安装 systemd"; exit 1; }}
  $SUDO systemctl stop wragent.service 2>/dev/null || true
  $SUDO systemctl disable wragent.service 2>/dev/null || true
  for u in $($SUDO systemctl list-unit-files 'wragent-*.service' --no-legend 2>/dev/null | awk '{{print $1}}'); do
    $SUDO systemctl stop "$u" 2>/dev/null || true
    $SUDO systemctl disable "$u" 2>/dev/null || true
  done
  $SUDO rm -f /etc/systemd/system/wragent.service /etc/systemd/system/wragent-*.service
  $SUDO systemctl daemon-reload || true
fi

# 配置: 已存在则复用（保持 identity/token，幂等）；不存在则新建，agent_id 留空由 wragent 自动生成
if [ ! -f /opt/wragent/config/config.json ]; then
  $SUDO tee /opt/wragent/config/config.json > /dev/null << EOF
{{
  "server_url": "$SERVER_URL",
  "agent_id": "",
  "auth_token": ""
}}
EOF
  echo "已生成空配置（Agent ID 将由 wragent 自动生成）"
else
  echo "复用已有配置 /opt/wragent/config/config.json"
fi

if [ "$METHOD" = "docker" ]; then
  $SUDO docker run -d \
    --name wragent \
    --network host \
    --restart unless-stopped \
    -v /opt/wragent/config:/config:rw \
    -v /opt/wragent/wragent:/src/wragent:ro \
    alpine:3.20 \
    sh -c "cp /src/wragent /usr/local/bin/wragent && chmod +x /usr/local/bin/wragent && exec /usr/local/bin/wragent -config /config/config.json"
  sleep 3
  echo ""
  echo "=== Agent 已启动 (Docker) ==="
  $SUDO docker ps --filter "name=wragent" --format "{{{{.Names}}}} {{{{.Status}}}}" || true
  echo "绑定: 请到门户「系统管理 → Agent 管理 → 待注册 Agent」审批"
  $SUDO docker logs wragent --tail 30 2>&1 | grep -iE "visit|authenticate|setup|认证|agent/s/" | tail -5 || true
  echo "日志: $SUDO docker logs wragent -f"
else
  $SUDO tee /etc/systemd/system/wragent.service > /dev/null << SVCEOF
[Unit]
Description=WRAgent
After=network.target
[Service]
Type=simple
ExecStart=/opt/wragent/wragent -config /opt/wragent/config/config.json
Restart=always
RestartSec=5
Environment=TZ=Asia/Shanghai
[Install]
WantedBy=multi-user.target
SVCEOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable wragent
  $SUDO systemctl restart wragent
  sleep 3
  echo ""
  echo "=== Agent 已启动 (systemd) ==="
  $SUDO systemctl status wragent --no-pager | head -6 || true
  echo "绑定: 请到门户「系统管理 → Agent 管理 → 待注册 Agent」审批"
  $SUDO journalctl -u wragent -n 40 --no-pager 2>/dev/null | grep -iE "visit|authenticate|setup|认证|agent/s/" | tail -5 || true
  echo "日志: $SUDO journalctl -u wragent -f"
fi
echo ""
echo "=== 安装完成 ==="
'''


# 模式二签名 URL 一次性消费记录: jti -> used_at
_deploy_keys_used: dict[str, float] = {}
DEPLOY_KEY_TTL = 1800  # 30 分钟


async def _resolve_deploy_key(k: str, agent_id: str, db: AsyncSession) -> str:
    """校验一次性部署 key(k)，返回该 Agent 的真实 token。"""
    try:
        payload = _decode_token(k)
    except HTTPException:
        raise HTTPException(status_code=400, detail="invalid deploy key")
    if payload.get("purpose") != "deploy" or payload.get("sub") != agent_id:
        raise HTTPException(status_code=400, detail="invalid deploy key")
    jti = payload.get("jti", "")
    now = time.time()
    for _j, _t in list(_deploy_keys_used.items()):
        if now - _t > 3600:
            _deploy_keys_used.pop(_j, None)
    if jti and jti in _deploy_keys_used:
        raise HTTPException(status_code=400, detail="deploy key already used")
    if jti:
        _deploy_keys_used[jti] = now
    result = await db.execute(select(Agent).where(Agent.id == agent_id))
    agent = result.scalar_one_or_none()
    if not agent or not agent.token:
        raise HTTPException(status_code=404, detail="agent not found")
    return agent.token


@deploy_router.get("/agent")
async def deploy_agent_script(method: str = "docker", id: str = "", k: str = "",
                              request: Request = None, db: AsyncSession = Depends(get_db)):
    if not id:
        raise HTTPException(status_code=400, detail="Agent ID is required")
    config = _get_deploy_config(request)
    token = ""
    if k:
        token = await _resolve_deploy_key(k, id, db)
    if method == "pm2":
        raise HTTPException(status_code=400, detail="PM2 method is no longer supported, use docker or systemd")
    elif method == "systemd":
        script = _generate_systemd_script(id, config, token)
    else:
        script = _generate_docker_script(id, config, token)
    return PlainTextResponse(content=script, media_type="text/plain")


class DeploySignedReq(BaseModel):
    method: str = "docker"
    id: str = ""


@deploy_router.post("/agent/signed-url")
async def deploy_signed_url(req: DeploySignedReq,
                            user: dict = Depends(require_permission("agent:manage")),
                            db: AsyncSession = Depends(get_db)):
    """模式二：为已创建的 Agent 签发 30 分钟一次性签名部署 URL（内嵌真实 token）。"""
    if not req.id:
        raise HTTPException(status_code=400, detail="Agent ID is required")
    result = await db.execute(select(Agent).where(Agent.id == req.id))
    agent = result.scalar_one_or_none()
    if not agent or not agent.token:
        raise HTTPException(status_code=404, detail="agent not found")
    jti = secrets.token_hex(8)
    now = int(time.time())
    payload = {"sub": req.id, "purpose": "deploy", "jti": jti, "iat": now, "exp": now + DEPLOY_KEY_TTL}
    k = jwt.encode(payload, JWT_SECRET, algorithm=JWT_ALGORITHM)
    return {
        "url": f"/api/deploy/agent?method={req.method}&id={req.id}&k={k}",
        "expires_in": DEPLOY_KEY_TTL,
    }


@deploy_router.get("/install-agent")
def install_agent_script(method: str = "", request: Request = None):
    """模式一公开安装脚本（Tailscale 式，无账户）。根路径别名见 /install-agent。"""
    config = _get_deploy_config(request)
    script = _generate_install_script(config, method)
    return PlainTextResponse(content=script, media_type="text/plain")


@admin_router.get("/agents/pending")
async def admin_pending_agents(user: dict = Depends(get_current_user)):
    """待注册(setup) Agent 列表：任何登录用户可见，审批后归属当前账户。"""
    from .api_isolated import _setup_sessions
    now = time.time()
    items = []
    for sid, s in _setup_sessions.items():
        if now - s.get("created_at", 0) < 1800:
            items.append({
                "sid": sid,
                "agent_id": s.get("agent_id", ""),
                "agent_name": s.get("agent_name", s.get("agent_id", "")),
                "created_at": s.get("created_at", 0),
            })
    items.sort(key=lambda x: x["created_at"])
    return {"pending": items}


@deploy_router.get("/wragent")
def deploy_wragent_binary(arch: str = "amd64"):
    base = os.environ.get("WRAGENT_DIR", "/app/wragent")
    mapping = {
        "amd64": "wragent",
        "arm64": "wragent-arm64",
    }
    filename = mapping.get(arch)
    if not filename:
        raise HTTPException(status_code=400, detail="unsupported arch")
    path = os.path.join(base, filename)
    if os.path.isfile(path):
        from fastapi.responses import FileResponse
        return FileResponse(path=path, filename=f"wragent-{arch}", media_type="application/octet-stream")
    raise HTTPException(status_code=404, detail="wragent binary not found")


@deploy_router.get("/wragent/{platform}")
def download_wragent(platform: str):
    """下载 wragent 二进制文件 (linux-amd64 / windows-amd64)"""
    base = os.environ.get("WRAGENT_DIR", "/app/wragent")
    mapping = {
        "linux-amd64": "wragent",
        "linux-arm64": "wragent-arm64",
        "windows-amd64": "wragent.exe",
    }
    filename = mapping.get(platform)
    if not filename:
        raise HTTPException(status_code=400, detail="unsupported platform")
    path = os.path.join(base, filename)
    if os.path.isfile(path):
        from fastapi.responses import FileResponse
        ext = ".exe" if "windows" in platform else ""
        return FileResponse(path=path, filename=f"wragent-{platform}{ext}", media_type="application/octet-stream")
    raise HTTPException(status_code=404, detail="binary not found")


@deploy_router.get("/wrgateway/{platform}")
def download_wrgateway(platform: str):
    """下载 wrgateway 二进制文件 (linux-amd64 / windows-amd64)"""
    base = os.environ.get("WRGATEWAY_DIR", "/app/wrgateway")
    mapping = {
        "linux-amd64": "wrgateway",
        "windows-amd64": "wrgateway.exe",
    }
    filename = mapping.get(platform)
    if not filename:
        raise HTTPException(status_code=400, detail="unsupported platform")
    path = os.path.join(base, filename)
    if os.path.exists(path):
        from fastapi.responses import FileResponse
        ext = ".exe" if "windows" in platform else ""
        return FileResponse(path=path, filename=f"wrgateway-{platform}{ext}", media_type="application/octet-stream")
    raise HTTPException(status_code=404, detail="binary not found")


# ════════════════════════════════════════════════════════════
#  网关管理 API
# ════════════════════════════════════════════════════════════

from .models import GatewayAddReq, GatewayUpdateReq


@admin_router.get("/gateway-id/next")
async def admin_next_gateway_id(user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    today = datetime.now(timezone.utc).strftime("%Y%m%d")
    prefix = f"gw-{today}"
    result = await db.execute(
        select(Gateway.id).where(Gateway.id.like(f"{prefix}%")).order_by(Gateway.id.desc())
    )
    existing_ids = [row[0] for row in result.all()]
    seq = 1
    for eid in existing_ids:
        try:
            num = int(eid.split(today)[1])
            if num >= seq:
                seq = num + 1
        except (IndexError, ValueError):
            pass
    return {"id": f"{prefix}{seq:02d}"}


@admin_router.get("/gateways")
async def admin_list_gateways(user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    uid = user["id"]
    result = await db.execute(select(Gateway).where(
        (Gateway.owner_id == uid) |
        (Gateway.shared_with == "all") |
        (Gateway.shared_with.contains(f'"{uid}"'))
    ).order_by(Gateway.created_at.desc()))
    gateways = result.scalars().all()
    owner_ids = list(set(g.owner_id for g in gateways if g.owner_id))
    user_map = {}
    if owner_ids:
        ur = await db.execute(select(User.id, User.username).where(User.id.in_(owner_ids)))
        user_map = {row[0]: row[1] for row in ur.all()}
    from .api_isolated import _online_gateways, _check_version_upgrade
    gw_list = []
    for g in gateways:
        online_info = _online_gateways.get(g.id, {})
        owner_name = user_map.get(g.owner_id, "")
        is_owner = g.owner_id == uid
        _online_ver = online_info.get("version") or ""
        _ver = g.version if _online_ver in ("", "unknown") else _online_ver
        _needs_upgrade, _latest_version = _check_version_upgrade("gateway", _ver)
        gw_list.append({
            "id": g.id, "name": g.name, "url": g.url, "token": g.token,
            "remark": g.remark, "is_active": g.is_active,
            "online": bool(online_info),
            "owner_id": g.owner_id, "owner_name": owner_name if not is_owner else "",
            "is_owner": is_owner, "shared_with": g.shared_with,
            "version": _ver,
            "needs_upgrade": _needs_upgrade,
            "latest_version": _latest_version,
        })
    return {"gateways": gw_list}


@admin_router.post("/gateways")
async def admin_add_gateway(req: GatewayAddReq, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    existing = await db.execute(select(Gateway).where(Gateway.id == req.id))
    if existing.scalar_one_or_none():
        raise HTTPException(status_code=400, detail="Gateway ID already exists")
    token = secrets.token_hex(32)
    db.add(Gateway(
        id=req.id, name=req.name, url=req.url, token=token,
        remark=req.remark, is_active=True,
        created_at=datetime.now(timezone.utc).isoformat(),
        owner_id=user["id"],
    ))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "gateway.create", "gateway", req.id, req.name, ip)
    return {"id": req.id, "token": token, "name": req.name}


@admin_router.put("/gateways/{gateway_id}")
async def admin_update_gateway(gateway_id: str, req: GatewayUpdateReq, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = result.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    if gw.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_edit_others_gateway"))
    if req.name is not None: gw.name = req.name
    if req.url is not None: gw.url = req.url
    if req.remark is not None: gw.remark = req.remark
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "gateway.update", "gateway", gateway_id, gw.name, ip)
    return {"ok": True}


@admin_router.delete("/gateways/{gateway_id}")
async def admin_delete_gateway(gateway_id: str, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = result.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    if gw.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_delete_others_gateway"))
    await db.execute(delete(Gateway).where(Gateway.id == gateway_id))
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "gateway.delete", "gateway", gateway_id, gw.name, ip)
    return {"ok": True}


@admin_router.post("/gateways/{gateway_id}/token")
async def admin_regenerate_gateway_token(gateway_id: str, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = result.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    if gw.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_gateway"))
    new_token = secrets.token_hex(32)
    gw.token = new_token
    await db.commit()
    ip = request.client.host if request.client else ""
    await log_audit(db, user["id"], user["username"], "gateway.token_regenerate", "gateway", gateway_id, gw.name, ip)
    return {"token": new_token}


@admin_router.post("/gateways/{gateway_id}/status")
async def admin_toggle_gateway_status(gateway_id: str, request: Request, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = result.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    if gw.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_gateway"))
    gw.is_active = not gw.is_active
    await db.commit()
    ip = request.client.host if request.client else ""
    action = "gateway.enable" if gw.is_active else "gateway.disable"
    await log_audit(db, user["id"], user["username"], action, "gateway", gateway_id, gw.name, ip)
    return {"ok": True, "is_active": gw.is_active}


@admin_router.post("/gateways/{gateway_id}/share")
async def admin_share_gateway(gateway_id: str, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db), body: dict = None):
    result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = result.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    if gw.owner_id != user["id"]:
        raise HTTPException(status_code=403, detail=t("admin.no_operate_others_gateway"))
    import json
    body = body or {}
    shared_with = body.get("shared_with", "private")
    if isinstance(shared_with, list):
        gw.shared_with = json.dumps(shared_with)
    else:
        gw.shared_with = str(shared_with)
    await db.commit()
    return {"ok": True, "shared_with": gw.shared_with}


@admin_router.get("/gateways/{gateway_id}/shares")
async def admin_get_gateway_shares(gateway_id: str, user: dict = Depends(require_permission("agent:manage")), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
    gw = result.scalar_one_or_none()
    if not gw:
        raise HTTPException(status_code=404, detail="Gateway not found")
    import json
    shared_with = gw.shared_with
    usernames = []
    if shared_with and shared_with not in ("private", "all"):
        try:
            uid_list = json.loads(shared_with)
            if uid_list:
                ur = await db.execute(select(User.id, User.username).where(User.id.in_(uid_list)))
                usernames = [{"id": row[0], "username": row[1]} for row in ur.all()]
        except Exception:
            pass
    return {"shared_with": shared_with, "users": usernames}

"""
用户隔离的API路由
为每个用户提供独立的数据存储空间
"""
import asyncio
import atexit
import collections
import errno
import json
import logging
import os
import posixpath
import queue
import uuid
import stat as _sftp_stat
from datetime import datetime, timezone
from logging.handlers import QueueHandler, QueueListener
from pathlib import Path
from typing import Optional

import asyncssh
from fastapi import APIRouter, HTTPException, UploadFile, File, WebSocket, WebSocketDisconnect, Depends, Request
from fastapi.responses import JSONResponse, StreamingResponse, PlainTextResponse
from pydantic import BaseModel

from fastapi import APIRouter, HTTPException, UploadFile, File, WebSocket, WebSocketDisconnect, Depends
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel
from sqlalchemy import select, update, delete, text, text
from sqlalchemy.ext.asyncio import AsyncSession

from .database import get_db, Document, LinkGroup, LinkItem, SshConnection, SftpConfig, Agent, User, CoturnServer, Gateway
from .auth import get_current_user, get_user_data_dir, get_ice_servers, require_permission
from .crypto import encrypt_secret, decrypt_secret
from .minio_client import ensure_bucket, upload_doc, download_doc, delete_doc
from .models import (
    WriteFileReq, NewEntryReq, RenameReq, PathReq, MoveReq,
    LinkItemReq, GroupAddReq, GroupRenameReq, GroupDeleteReq, LinkDeleteReq,
    SshConn, SshDeleteReq, SshTestReq,
    SftpClientReq, SftpWriteReq, SftpMkdirReq, SftpRenameReq, SftpChmodReq,
)
from .i18n import t

router = APIRouter(prefix="/api")
agent_router = APIRouter()  # 无前缀, 用于 /agent/s/{sid}

MD_ROOT = Path(os.environ.get("MD_ROOT", str(Path(__file__).resolve().parent.parent / "md"))).resolve()
CONFIG_ROOT = Path(os.environ.get("CONFIG_DIR", str(Path(__file__).resolve().parent.parent / "config"))).resolve()
LOGS_DIR = Path(os.environ.get("LOGS_DIR", str(Path(__file__).resolve().parent.parent / "logs"))).resolve()
LOGS_DIR.mkdir(parents=True, exist_ok=True)

# ── Logging ──────────────────────────────────────────────
#
# 事件循环里绝不能做同步磁盘 I/O：FileHandler 是同步的，每条日志都会卡住
# 一次事件循环（信令路径上 ICE candidate/answer 高频打点时尤其明显）。
# 这里改用 QueueHandler + QueueListener，把格式化与写盘挪到后台线程。

_log_level = os.environ.get("APP_LOG_LEVEL", "INFO").upper()

_logger = logging.getLogger("app")
_logger.setLevel(getattr(logging, _log_level, logging.INFO))

_fmt = logging.Formatter("%(asctime)s [%(levelname)s] %(name)s: %(message)s", datefmt="%Y-%m-%d %H:%M:%S")

_fh = logging.FileHandler(LOGS_DIR / "backend.log", encoding="utf-8")
_fh.setLevel(getattr(logging, _log_level, logging.INFO))
_fh.setFormatter(_fmt)

_log_q: "queue.Queue[logging.LogRecord]" = queue.Queue(-1)
_qh = QueueHandler(_log_q)
_qh.setFormatter(_fmt)
_logger.addHandler(_qh)

_log_listener = QueueListener(_log_q, _fh, respect_handler_level=True)
_log_listener.start()
atexit.register(_log_listener.stop)

_ch = logging.StreamHandler()
_ch.setLevel(logging.INFO)
_ch.setFormatter(_fmt)
_logger.addHandler(_ch)

_logger.info("CONFIG_ROOT=%s", CONFIG_ROOT)
_logger.info("MD_ROOT=%s", MD_ROOT)
_logger.info("LOGS_DIR=%s", LOGS_DIR)

SFTP_HOST_KEY = "sftp_host_key"

# ── 数据库辅助函数 ───────────────────────────


async def _read_user_ssh_db(db: AsyncSession, user_id: str) -> list[dict]:
    result = await db.execute(
        select(SshConnection).where(SshConnection.user_id == user_id).order_by(SshConnection.sort_order.asc(), SshConnection.created_at.desc())
    )
    rows = result.scalars().all()
    return [
        {
            "id": r.id, "name": r.name, "host": r.host, "port": r.port,
            "username": r.username, "auth_type": r.auth_type,
            "password": decrypt_secret(r.password),
            "key_path": r.key_path, "connection_mode": r.connection_mode,
            "agent_id": r.agent_id, "remark": r.remark, "created_at": r.created_at,
            "connection_type": r.connection_type, "vnc_port": r.vnc_port,
            "vnc_password": decrypt_secret(r.vnc_password),
            "pixel_format": r.pixel_format,
            "color_depth": r.color_depth, "read_only": r.read_only, "gateway_id": r.gateway_id,
            "sort_order": r.sort_order,
            "rdp_port": r.rdp_port,
            "rdp_password": decrypt_secret(r.rdp_password),
            "rdp_domain": r.rdp_domain,
            "rdp_resolution": r.rdp_resolution,
        }
        for r in rows
    ]


async def _write_user_ssh_list(db: AsyncSession, user_id: str, data: list[dict]):
    await db.execute(delete(SshConnection).where(SshConnection.user_id == user_id))
    for s in data:
        db.add(SshConnection(
            id=s.get("id", ""), user_id=user_id,
            name=s.get("name", ""), host=s.get("host", ""),
            port=s.get("port", 22), username=s.get("username", ""),
            auth_type=s.get("auth_type", s.get("authType", "password")),
            password=encrypt_secret(s.get("password", "")),
            key_path=s.get("key_path", s.get("keyPath", "")), connection_mode=s.get("connection_mode", "agent"),
            agent_id=s.get("agent_id", ""), remark=s.get("remark", ""),
            created_at=s.get("created_at", s.get("createdAt", datetime.now(timezone.utc).isoformat())),
            connection_type=s.get("connection_type", "ssh"),
            vnc_port=s.get("vnc_port", 5900),
            vnc_password=encrypt_secret(s.get("vnc_password", "")),
            pixel_format=s.get("pixel_format", "tight"), color_depth=s.get("color_depth", "full"),
            read_only=s.get("read_only", False), gateway_id=s.get("gateway_id", ""),
            rdp_port=s.get("rdp_port", 3389),
            rdp_password=encrypt_secret(s.get("rdp_password", "")),
            rdp_domain=s.get("rdp_domain", ""),
            rdp_resolution=s.get("rdp_resolution", "1920x1080"),
        ))
    await db.commit()


async def _read_user_sftp_config_db(db: AsyncSession, user_id: str) -> dict:
    result = await db.execute(select(SftpConfig).where(SftpConfig.user_id == user_id, SftpConfig.scope == "per_user"))
    cfg = result.scalar_one_or_none()
    if not cfg:
        return {
            "share_dir": str(MD_ROOT), "read_only": True,
            "username": "ppy", "password": "changeme", "port": 2222, "bind": "0.0.0.0",
        }
    return {
        "share_dir": cfg.share_dir, "read_only": cfg.read_only,
        "username": cfg.username, "password": decrypt_secret(cfg.password),
        "port": cfg.port, "bind": cfg.bind,
    }


async def _write_user_sftp_config_db(db: AsyncSession, user_id: str, data: dict):
    result = await db.execute(select(SftpConfig).where(SftpConfig.user_id == user_id, SftpConfig.scope == "per_user"))
    cfg = result.scalar_one_or_none()
    if cfg:
        cfg.share_dir = data.get("share_dir", cfg.share_dir)
        cfg.read_only = data.get("read_only", cfg.read_only)
        cfg.username = data.get("username", cfg.username)
        if "password" in data:
            cfg.password = encrypt_secret(data.get("password", ""))
        cfg.port = data.get("port", cfg.port)
        cfg.bind = data.get("bind", cfg.bind)
    else:
        db.add(SftpConfig(
            id=f"sftp_user_{user_id}", scope="per_user", user_id=user_id,
            share_dir=data.get("share_dir", str(MD_ROOT)),
            read_only=data.get("read_only", True),
            username=data.get("username", "ppy"),
            password=encrypt_secret(data.get("password", "changeme")),
            port=data.get("port", 2222),
            bind=data.get("bind", "0.0.0.0"),
        ))
    await db.commit()


async def _upsert_agent_to_db(agent_id: str, agent_name: str, version: str = "", deploy_mode: str = ""):
    """wragent 注册时自动在数据库中创建/更新 agent 记录"""
    import secrets
    async with get_db_session() as db:
        result = await db.execute(select(Agent).where(Agent.id == agent_id))
        agent = result.scalar_one_or_none()
        if agent:
            # 保留管理台命名的显示名: 仅当行名为原始 agent_id(未改名) 或为空时才跟随上报名
            if (agent.name or "") == agent.id or not agent.name:
                agent.name = agent_name
            if version:
                agent.version = version
            if deploy_mode:
                agent.deploy_mode = deploy_mode
        else:
            db.add(Agent(
                id=agent_id, name=agent_name,
                token=secrets.token_hex(32),
                remark="自动注册", is_active=True,
                created_at=datetime.now(timezone.utc).isoformat(),
                version=version,
                deploy_mode=deploy_mode,
            ))
        await db.commit()


async def _get_agent_token_from_db(agent_id: str) -> str:
    """从 DB 获取 agent 的 token"""
    async with get_db_session() as db:
        result = await db.execute(select(Agent.token).where(Agent.id == agent_id))
        row = result.scalar_one_or_none()
        return row or ""


async def _get_gateway_token_from_db(gateway_id: str) -> str:
    """从 DB 获取 gateway 的 token"""
    async with get_db_session() as db:
        result = await db.execute(select(Gateway.token).where(Gateway.id == gateway_id))
        row = result.scalar_one_or_none()
        return row or ""


async def _upsert_gateway_to_db(gateway_id: str, gateway_name: str, version: str = ""):
    """wrgateway 注册时更新数据库中已存在 gateway 的版本信息 (仅更新, 不自动创建)"""
    async with get_db_session() as db:
        result = await db.execute(select(Gateway).where(Gateway.id == gateway_id))
        gateway = result.scalar_one_or_none()
        if gateway:
            # 仅当显式传入 gateway_name 且与 id 不同时才更新显示名，避免用 id 覆盖中文名
            if gateway_name and gateway_name != gateway_id:
                gateway.name = gateway_name
            if version:
                gateway.version = version
            await db.commit()


def _compare_versions(cur: str, latest: str) -> bool:
    """返回当前版本是否落后于最新版本 (semver 比较)"""
    def _norm(v: str):
        parts = []
        for p in v.strip().lstrip("v").split("."):
            try:
                parts.append(int(p))
            except ValueError:
                parts.append(0)
        while len(parts) < 3:
            parts.append(0)
        return tuple(parts[:3])
    return _norm(cur) < _norm(latest)


def _check_version_upgrade(role: str, cur_version: str):
    """对比版本, 返回 (needs_upgrade, latest_version)"""
    latest = os.environ.get(f"LATEST_{role.upper()}_VERSION", "")
    if not latest or not cur_version or cur_version == "dev" or cur_version == latest:
        return False, latest
    return _compare_versions(cur_version, latest), latest


# context manager for DB sessions outside of FastAPI DI
from contextlib import asynccontextmanager

@asynccontextmanager
async def get_db_session():
    import app.database as _dbmod
    if _dbmod._session_factory is None:
        await _dbmod.init_db()
    async with _dbmod._session_factory() as session:
        yield session


# ── 文件工具 ──────────────────────────────────────────


def _safe_path(rel: str) -> Path:
    target = (MD_ROOT / rel.lstrip("/")).resolve()
    if not target.is_relative_to(MD_ROOT):
        raise HTTPException(status_code=403, detail=t("file.path_traversal"))
    return target


def _scan_tree(root: Path) -> list[dict]:
    nodes: list[dict] = []
    try:
        entries = sorted(root.iterdir(), key=lambda p: (p.is_file(), p.name.casefold()))
    except OSError:
        return nodes
    ALLOWED_EXTS = {".md", ".txt", ".markdown"}
    SKIP_FILES = {SFTP_HOST_KEY}
    for p in entries:
        if p.name.startswith(".") or p.name in SKIP_FILES:
            continue
        rel = p.relative_to(MD_ROOT).as_posix()
        if p.is_dir():
            children = _scan_tree(p)
            nodes.append({"name": p.name, "path": rel, "type": "dir", "children": children})
        elif p.suffix.lower() in ALLOWED_EXTS:
            nodes.append({"name": p.name, "path": rel, "type": "file"})
    return nodes


# ═══════════════════════════════════════════════════════════
#  文档管理 API (需要认证，用户隔离，MinIO 存储)
# ═══════════════════════════════════════════════════════════

MAX_DOC_SIZE = 5 * 1024 * 1024  # 5MB


def _build_doc_tree(docs: list[Document]) -> list[dict]:
    """将扁平文档列表构建为树形结构"""
    by_id: dict[str, dict] = {}
    children_map: dict[str | None, list[dict]] = {}
    for d in docs:
        node = {"name": d.name, "path": d.path, "type": "dir" if d.is_dir else "file", "id": d.id}
        by_id[d.id] = node
        children_map.setdefault(d.parent_id, []).append(node)
    for d in docs:
        node = by_id[d.id]
        if node["type"] == "dir":
            kids = children_map.get(d.id, [])
            kids.sort(key=lambda x: (x["type"] != "dir", x["name"].casefold()))
            node["children"] = kids
        else:
            node["children"] = None
    roots = children_map.get(None, [])
    roots.sort(key=lambda x: (x["type"] != "dir", x["name"].casefold()))
    return roots


async def _ensure_doc_dirs(db: AsyncSession, user_id: str, path: str) -> str | None:
    """确保路径中所有父目录都存在，返回根目录 ID"""
    parts = [p for p in path.strip("/").split("/") if p]
    if not parts:
        return None
    parent_id = None
    for part in parts[:-1]:
        result = await db.execute(
            select(Document).where(Document.user_id == user_id, Document.path == "/".join(parts[:parts.index(part) + 1]), Document.is_dir == True)
        )
        d = result.scalar_one_or_none()
        if not d:
            now = datetime.now(timezone.utc).isoformat()
            dir_id = f"doc_{uuid.uuid4().hex[:12]}"
            d = Document(id=dir_id, user_id=user_id, name=part,
                         path="/".join(parts[:parts.index(part) + 1]),
                         is_dir=True, parent_id=parent_id, created_at=now)
            db.add(d)
            await db.flush()
        parent_id = d.id
    return parent_id


@router.get("/docs/tree")
async def docs_tree(user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(Document).where(Document.user_id == user["id"]))
    docs = list(result.scalars().all())
    return _build_doc_tree(docs)


@router.get("/docs/content")
async def docs_content(path: str, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    result = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == path, Document.is_dir == False)
    )
    doc = result.scalar_one_or_none()
    if not doc or not doc.minio_key:
        raise HTTPException(status_code=404, detail=t("file.not_found"))
    try:
        content = download_doc(doc.minio_key)
        return {"content": content.decode("utf-8")}
    except Exception as e:
        raise HTTPException(status_code=500, detail=t("file.read_failed", error=e))


@router.post("/docs/write")
async def docs_write(req: WriteFileReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    content_bytes = req.content.encode("utf-8")
    if len(content_bytes) > MAX_DOC_SIZE:
        raise HTTPException(status_code=413, detail=t("file.size_exceed", max_mb=MAX_DOC_SIZE // 1024 // 1024))
    parts = [p for p in req.path.strip("/").split("/") if p]
    if not parts:
        raise HTTPException(status_code=400, detail=t("file.invalid_path"))
    filename = parts[-1]
    now = datetime.now(timezone.utc).isoformat()
    result = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == req.path)
    )
    doc = result.scalar_one_or_none()
    if doc:
        if doc.is_dir:
            raise HTTPException(status_code=400, detail=t("file.cannot_write_dir"))
        if doc.minio_key:
            try:
                delete_doc(doc.minio_key)
            except Exception:
                pass
        doc.minio_key = upload_doc(user["id"], doc.id, filename, content_bytes)
        doc.size = len(content_bytes)
        doc.updated_at = now
    else:
        parent_id = None
        if len(parts) > 1:
            parent_path = "/".join(parts[:-1])
            pr = await db.execute(
                select(Document).where(Document.user_id == user["id"], Document.path == parent_path, Document.is_dir == True)
            )
            parent_doc = pr.scalar_one_or_none()
            if not parent_doc:
                parent_id = await _ensure_doc_dirs(db, user["id"], req.path)
            else:
                parent_id = parent_doc.id
        doc_id = f"doc_{uuid.uuid4().hex[:12]}"
        minio_key = upload_doc(user["id"], doc_id, filename, content_bytes)
        doc = Document(id=doc_id, user_id=user["id"], name=filename, path=req.path,
                       is_dir=False, parent_id=parent_id, size=len(content_bytes),
                       minio_key=minio_key, created_at=now, updated_at=now)
        db.add(doc)
    await db.commit()
    return {"ok": True, "path": req.path}


@router.post("/docs/mkdir")
async def docs_mkdir(req: PathReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    path = req.path
    parts = [p for p in path.strip("/").split("/") if p]
    if not parts:
        raise HTTPException(status_code=400, detail=t("file.invalid_path"))
    result = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == path)
    )
    if result.scalar_one_or_none():
        raise HTTPException(status_code=409, detail=t("file.already_exists"))
    parent_id = None
    if len(parts) > 1:
        parent_path = "/".join(parts[:-1])
        pr = await db.execute(
            select(Document).where(Document.user_id == user["id"], Document.path == parent_path, Document.is_dir == True)
        )
        parent_doc = pr.scalar_one_or_none()
        if not parent_doc:
            parent_id = await _ensure_doc_dirs(db, user["id"], path)
        else:
            parent_id = parent_doc.id
    now = datetime.now(timezone.utc).isoformat()
    dir_id = f"doc_{uuid.uuid4().hex[:12]}"
    db.add(Document(id=dir_id, user_id=user["id"], name=parts[-1], path=path,
                    is_dir=True, parent_id=parent_id, created_at=now))
    await db.commit()
    return {"ok": True, "path": path}


@router.post("/docs/rename")
async def docs_rename(req: RenameReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    old_path = req.old_path
    new_name = req.new_name
    result = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == old_path)
    )
    doc = result.scalar_one_or_none()
    if not doc:
        raise HTTPException(status_code=404, detail=t("file.not_found"))
    parent_path = "/".join(doc.path.rstrip("/").split("/")[:-1])
    new_path = f"{parent_path}/{new_name}" if parent_path else new_name
    dup = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == new_path, Document.id != doc.id)
    )
    if dup.scalar_one_or_none():
        raise HTTPException(status_code=409, detail=t("file.target_exists"))
    old_prefix = doc.path.rstrip("/") + "/"
    doc.name = new_name
    doc.path = new_path
    if doc.is_dir:
        sub = await db.execute(
            select(Document).where(Document.user_id == user["id"], Document.path.startswith(old_prefix))
        )
        for child in sub.scalars().all():
            child.path = new_path + "/" + child.path[len(old_prefix):]
    await db.commit()
    return {"ok": True, "path": new_path}


@router.post("/docs/delete")
async def docs_delete(req: PathReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    path = req.path
    result = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == path)
    )
    doc = result.scalar_one_or_none()
    if not doc:
        raise HTTPException(status_code=404, detail=t("file.not_found"))
    paths_to_delete = [doc.path]
    if doc.is_dir:
        prefix = doc.path.rstrip("/") + "/"
        sub = await db.execute(
            select(Document).where(Document.user_id == user["id"], Document.path.startswith(prefix))
        )
        for child in sub.scalars().all():
            paths_to_delete.append(child.path)
    all_docs = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path.in_(paths_to_delete))
    )
    for d in all_docs.scalars().all():
        if d.minio_key:
            try:
                delete_doc(d.minio_key)
            except Exception:
                pass
        await db.delete(d)
    await db.commit()
    return {"ok": True}


@router.post("/docs/move")
async def docs_move(req: MoveReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    src = req.src
    dst_dir = req.dst_dir
    result = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == src)
    )
    doc = result.scalar_one_or_none()
    if not doc:
        raise HTTPException(status_code=404, detail=t("file.source_not_found"))
    dst = None
    if dst_dir:
        dst_dir_doc = await db.execute(
            select(Document).where(Document.user_id == user["id"], Document.path == dst_dir, Document.is_dir == True)
        )
        dst = dst_dir_doc.scalar_one_or_none()
        if not dst:
            raise HTTPException(status_code=404, detail=t("file.target_dir_not_found"))
    new_path = doc.name if not dst_dir else f"{dst_dir.rstrip('/')}/{doc.name}"
    dup = await db.execute(
        select(Document).where(Document.user_id == user["id"], Document.path == new_path, Document.id != doc.id)
    )
    if dup.scalar_one_or_none():
        raise HTTPException(status_code=409, detail=t("file.target_file_exists"))
    old_prefix = doc.path.rstrip("/") + "/"
    doc.parent_id = dst.id if dst else None
    doc.path = new_path
    if doc.is_dir:
        sub = await db.execute(
            select(Document).where(Document.user_id == user["id"], Document.path.startswith(old_prefix))
        )
        for child in sub.scalars().all():
            child.path = new_path + "/" + child.path[len(old_prefix):]
    await db.commit()
    return {"ok": True, "path": new_path}


# ═══════════════════════════════════════════════════════════
#  链接管理 API (需要认证，用户隔离，关系型 DB)
# ═══════════════════════════════════════════════════════════


@router.get("/links")
async def links_list(user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    uid = user["id"]
    gr = await db.execute(select(LinkGroup).where(LinkGroup.user_id == uid).order_by(LinkGroup.sort_order))
    groups = list(gr.scalars().all())
    lr = await db.execute(select(LinkItem).where(LinkItem.user_id == uid))
    items = list(lr.scalars().all())
    items_by_group: dict[str, list] = {}
    for item in items:
        items_by_group.setdefault(item.group_id, []).append({
            "id": item.id, "title": item.title, "url": item.url,
            "description": item.description, "group_id": item.group_id,
        })
    return {
        "groups": [
            {
                "id": g.id, "name": g.name,
                "links": items_by_group.get(g.id, []),
            }
            for g in groups
        ]
    }


@router.post("/links/add")
async def links_add(item: LinkItemReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    uid = user["id"]
    gid = item.group_id
    if gid == "ungrouped":
        gr = await db.execute(select(LinkGroup).where(LinkGroup.user_id == uid, LinkGroup.name == "未分组").limit(1))
        g = gr.scalar_one_or_none()
        if not g:
            g = LinkGroup(id=f"lg_{uuid.uuid4().hex[:8]}", user_id=uid, name="未分组", created_at=datetime.now(timezone.utc).isoformat())
            db.add(g)
            await db.flush()
        gid = g.id
    link_id = f"li_{uuid.uuid4().hex[:8]}"
    db.add(LinkItem(id=link_id, user_id=uid, group_id=gid, title=item.title,
                    url=item.url, description=item.description, created_at=datetime.now(timezone.utc).isoformat()))
    await db.commit()
    return {"ok": True, "item": {"id": link_id, "title": item.title, "url": item.url, "description": item.description, "group_id": gid}}


@router.post("/links/update")
async def links_update(item: LinkItemReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    if not item.id:
        raise HTTPException(status_code=400, detail=t("link.missing_id"))
    result = await db.execute(select(LinkItem).where(LinkItem.id == item.id, LinkItem.user_id == user["id"]))
    link = result.scalar_one_or_none()
    if not link:
        raise HTTPException(status_code=404, detail=t("link.not_found"))
    link.title = item.title
    link.url = item.url
    link.description = item.description
    if item.group_id and item.group_id != link.group_id:
        link.group_id = item.group_id
    await db.commit()
    return {"ok": True}


@router.post("/links/delete")
async def links_delete(req: LinkDeleteReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(LinkItem).where(LinkItem.id == req.id, LinkItem.user_id == user["id"]))
    link = result.scalar_one_or_none()
    if not link:
        raise HTTPException(status_code=404, detail=t("link.not_found"))
    await db.delete(link)
    await db.commit()
    return {"ok": True}


@router.post("/links/group/add")
async def links_group_add(req: GroupAddReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    gid = f"lg_{uuid.uuid4().hex[:8]}"
    db.add(LinkGroup(id=gid, user_id=user["id"], name=req.name, created_at=datetime.now(timezone.utc).isoformat()))
    await db.commit()
    return {"ok": True, "id": gid}


@router.post("/links/group/rename")
async def links_group_rename(req: GroupRenameReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(LinkGroup).where(LinkGroup.id == req.id, LinkGroup.user_id == user["id"]))
    g = result.scalar_one_or_none()
    if not g:
        raise HTTPException(status_code=404, detail=t("link.group_not_found"))
    g.name = req.name
    await db.commit()
    return {"ok": True}


@router.post("/links/group/delete")
async def links_group_delete(req: GroupDeleteReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    result = await db.execute(select(LinkGroup).where(LinkGroup.id == req.id, LinkGroup.user_id == user["id"]))
    g = result.scalar_one_or_none()
    if not g:
        raise HTTPException(status_code=404, detail=t("link.group_not_found"))
    items = await db.execute(select(LinkItem).where(LinkItem.group_id == req.id))
    for item in items.scalars().all():
        await db.delete(item)
    await db.delete(g)
    await db.commit()
    return {"ok": True}


# ═══════════════════════════════════════════════════════════
#  SSH 管理 API (需要认证，用户隔离)
# ═══════════════════════════════════════════════════════════


class SshDeleteReq(BaseModel):
    id: str


class SshTestReq(BaseModel):
    host: str
    port: int = 22
    username: str
    auth_type: str = "password"
    password: str = ""
    key_path: str = ""


@router.get("/ssh")
async def ssh_list(user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    conns = await _read_user_ssh_db(db, user["id"])
    _logger.info("ssh_list: returning %d connections for user %s", len(conns), user["username"])
    safe = []
    for c in conns:
        s = {k: v for k, v in c.items() if k not in ("password", "vnc_password", "rdp_password")}
        s["has_password"] = bool(c.get("password")) or bool(c.get("vnc_password"))
        safe.append(s)
    return safe


@router.post("/ssh/reorder")
async def ssh_reorder(items: list[dict], user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    """拖拽排序：批量更新 sort_order"""
    for item in items:
        cid = item.get("id")
        order = item.get("sort_order", 0)
        if not cid:
            continue
        await db.execute(
            text("UPDATE ssh_connections SET sort_order = :order WHERE id = :id AND user_id = :uid"),
            {"order": order, "id": cid, "uid": user["id"]}
        )
    await db.commit()
    return {"ok": True}


@router.get("/ssh/{conn_id}/password")
async def ssh_get_password(conn_id: str, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    conns = await _read_user_ssh_db(db, user["id"])
    conn = next((c for c in conns if c.get("id") == conn_id), None)
    if not conn:
        raise HTTPException(status_code=404, detail=t("ssh.connection_not_found"))
    return {"password": conn.get("password", ""), "vnc_password": conn.get("vnc_password", ""), "rdp_password": conn.get("rdp_password", "")}


@router.post("/ssh/add")
async def ssh_add(conn: SshConn, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    data = await _read_user_ssh_db(db, user["id"])
    conn.id = str(uuid.uuid4())[:8]
    data.append(conn.model_dump())
    await _write_user_ssh_list(db, user["id"], data)
    _logger.info("ssh_add: %s@%s:%d id=%s user=%s", conn.username, conn.host, conn.port, conn.id, user["username"])
    return {"ok": True, "id": conn.id}


@router.post("/ssh/update")
async def ssh_update(conn: SshConn, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    if not conn.id:
        raise HTTPException(status_code=400, detail=t("ssh.missing_id"))
    data = await _read_user_ssh_db(db, user["id"])
    for i, c in enumerate(data):
        if c.get("id") == conn.id:
            if not conn.password:
                conn.password = c.get("password", "")
            if not conn.vnc_password:
                conn.vnc_password = c.get("vnc_password", "")
            if not conn.rdp_password:
                conn.rdp_password = c.get("rdp_password", "")
            data[i] = conn.model_dump()
            await _write_user_ssh_list(db, user["id"], data)
            _logger.info("ssh_update: id=%s %s@%s:%d user=%s", conn.id, conn.username, conn.host, conn.port, user["username"])
            return {"ok": True}
    raise HTTPException(status_code=404, detail=t("ssh.connection_not_found"))


@router.post("/ssh/delete")
async def ssh_delete(req: SshDeleteReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    data = await _read_user_ssh_db(db, user["id"])
    data = [c for c in data if c.get("id") != req.id]
    await _write_user_ssh_list(db, user["id"], data)
    return {"ok": True}


@router.post("/ssh/test")
async def ssh_test(req: SshTestReq, user: dict = Depends(get_current_user)):
    try:
        kwargs: dict = {"host": req.host, "port": req.port, "username": req.username}
        if req.auth_type == "password":
            kwargs["password"] = req.password
        else:
            kwargs["client_keys"] = [req.key_path] if req.key_path else []

        async def _do_connect():
            async with asyncssh.connect(**kwargs, known_hosts=None, keepalive_interval=30) as conn:
                pass

        await asyncio.wait_for(_do_connect(), timeout=8)
        return {"ok": True, "detail": t("ssh.connect_success")}
    except asyncio.TimeoutError:
        return {"ok": False, "detail": t("ssh.connect_timeout")}
    except Exception as e:
        return {"ok": False, "detail": str(e)}


class VncTestReq(BaseModel):
    host: str
    port: int = 5900


@router.post("/vnc/test")
async def vnc_test(req: VncTestReq, user: dict = Depends(get_current_user)):
    import socket
    import asyncio
    try:
        reader, writer = await asyncio.wait_for(
            asyncio.open_connection(req.host, req.port),
            timeout=5,
        )
        data = await asyncio.wait_for(reader.read(12), timeout=5)
        version = data.decode().strip()
        writer.close()
        await writer.wait_closed()
        return {"ok": True, "detail": t("vnc.reachable", version=version)}
    except asyncio.TimeoutError:
        return {"ok": False, "detail": t("vnc.connect_timeout", host=req.host, port=req.port)}
    except Exception as e:
        return {"ok": False, "detail": str(e)}


# ═══════════════════════════════════════════════════════════
#  SFTP 服务 (需要认证，用户隔离)
# ═══════════════════════════════════════════════════════════

SFTP_CONFIG_FILE = "sftp_config.json"

_sftp_server = None
_sftp_acceptor = None
_sftp_connections: list = []
_sftp_log: collections.deque = collections.deque(maxlen=100)


def _log_sftp(msg: str) -> None:
    ts = datetime.now().strftime("%H:%M:%S")
    _sftp_log.append(f"[{ts}] {msg}")


class SFTPSSHServer(asyncssh.SSHServer):
    def __init__(self, username: str, password: str):
        self._username = username
        self._password = password

    def get_server_host_key(self):
        return None

    def password_auth_supported(self):
        return True

    def public_key_auth_supported(self):
        return False

    def validate_password(self, username, password):
        return username == self._username and password == self._password


class SFTPSSHServerFactory:
    def __init__(self, username: str, password: str):
        self._username = username
        self._password = password

    def __call__(self):
        return SFTPSSHServer(self._username, self._password)


class SFTPSessionHandler(asyncssh.SSHServerChannel):
    def __init__(self, *args, fs=None, **kwargs):
        super().__init__(*args, **kwargs)
        self._sftp_fs = fs

    def session_requested(self):
        return "sftp"


async def _start_sftp_server(config: dict):
    global _sftp_acceptor
    share_dir = config.get("share_dir", str(MD_ROOT))
    username = config.get("username", "ppy")
    password = config.get("password", "changeme")
    port = config.get("port", 2222)
    bind = config.get("bind", "0.0.0.0")

    host_key_path = CONFIG_ROOT / SFTP_HOST_KEY
    if not host_key_path.is_file():
        key = asyncssh.generate_private_key("ssh-ed25519")
        key.write_private_key(str(host_key_path))
        os.chmod(str(host_key_path), 0o600)
        _log_sftp("已生成主机密钥")

    def server_factory():
        return SFTPSSHServer(username, password)

    _sftp_acceptor = await asyncssh.create_server(
        server_factory,
        host=bind,
        port=port,
        server_host_keys=[str(host_key_path)],
        sftp_factory=lambda chan: asyncssh.sftp.SFTPServer(chan, chroot=share_dir.encode("utf-8") if share_dir else None),
    )
    _log_sftp(f"SFTP 服务已启动 (端口 {port}, 目录 {share_dir})")
    return _sftp_acceptor


async def _stop_sftp_server():
    global _sftp_acceptor
    if _sftp_acceptor:
        _sftp_acceptor.close()
        await _sftp_acceptor.wait_closed()
        _sftp_acceptor = None
        _log_sftp("SFTP 服务已停止")


class SftpConfigReq(BaseModel):
    share_dir: str = ""
    read_only: bool = True
    username: str = "ppy"
    password: str = "changeme"
    port: int = 2222
    bind: str = "0.0.0.0"


@router.get("/sftp/config")
async def sftp_config_get(user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    return await _read_user_sftp_config_db(db, user["id"])


@router.post("/sftp/config")
async def sftp_config_save(req: SftpConfigReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    cfg = req.model_dump()
    await _write_user_sftp_config_db(db, user["id"], cfg)
    return {"ok": True}


@router.post("/sftp/start")
async def sftp_start(user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    global _sftp_acceptor
    if _sftp_acceptor:
        return {"ok": False, "detail": t("sftp.already_running")}
    cfg = await _read_user_sftp_config_db(db, user["id"])
    try:
        await _start_sftp_server(cfg)
        return {"ok": True}
    except Exception as e:
        return {"ok": False, "detail": str(e)}


@router.post("/sftp/stop")
async def sftp_stop(user: dict = Depends(get_current_user)):
    await _stop_sftp_server()
    return {"ok": True}


@router.get("/sftp/status")
def sftp_status(user: dict = Depends(get_current_user)):
    return {"running": _sftp_acceptor is not None}


@router.get("/sftp/log")
def sftp_log(user: dict = Depends(get_current_user)):
    return {"log": list(_sftp_log)}


# ═══════════════════════════════════════════════════════════
#  SFTP 客户端(远程文件管理)
# ═══════════════════════════════════════════════════════════


def _sftp_conn_kwargs(params: dict) -> dict:
    host = params.get("host", "")
    port = params.get("port", 22)
    username = params.get("username", "root")
    auth_type = params.get("auth_type", "password")
    password = params.get("password", "")
    key_path = params.get("key_path", "")
    conn_id = params.get("id", "")

    kw: dict = {"host": host, "port": port, "username": username, "known_hosts": None}

    if auth_type == "password":
        if not password and conn_id:
            # Try to look up from current user's saved connections
            pass
        if password:
            kw["password"] = password
    else:
        if key_path:
            kw["client_keys"] = [key_path]
        elif conn_id:
            pass
    return kw


SFTP_CONNECT_TIMEOUT = 10.0


class _SftpConnect:
    def __init__(self, kw: dict) -> None:
        self._kw = kw
        self._conn = None

    async def __aenter__(self):
        try:
            self._conn = await asyncio.wait_for(asyncssh.connect(**self._kw), timeout=SFTP_CONNECT_TIMEOUT)
        except TimeoutError as e:
            raise TimeoutError(t("sftp.connect_timeout")) from e
        return self._conn

    async def __aexit__(self, exc_type, exc, tb) -> bool:
        if self._conn is not None:
            self._conn.close()
            await self._conn.wait_closed()
        return False


def _sftp_connect(kw: dict) -> _SftpConnect:
    return _SftpConnect(kw)


def _format_mode(mode: int) -> str:
    if _sftp_stat.S_ISLNK(mode):
        prefix = "l"
    elif _sftp_stat.S_ISDIR(mode):
        prefix = "d"
    elif _sftp_stat.S_ISBLK(mode):
        prefix = "b"
    elif _sftp_stat.S_ISCHR(mode):
        prefix = "c"
    elif _sftp_stat.S_ISFIFO(mode):
        prefix = "p"
    elif _sftp_stat.S_ISSOCK(mode):
        prefix = "s"
    else:
        prefix = "-"
    perms = ""
    for shift in (6, 3, 0):
        bit = (mode >> shift) & 7
        perms += ("r" if bit & 4 else "-") + ("w" if bit & 2 else "-") + ("x" if bit & 1 else "-")
    return prefix + perms


@router.post("/sftp-client/list")
async def sftp_client_list(req: SftpClientReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    path = req.path or "/"
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                items = []
                async for entry in sftp.scandir(path):
                    name = entry.filename
                    if name in ('.', '..'):
                        continue
                    attrs = entry.attrs
                    mode = attrs.permissions if hasattr(attrs, "permissions") else 0
                    size = attrs.size if hasattr(attrs, "size") else 0
                    mtime_val = attrs.mtime if hasattr(attrs, "mtime") else None
                    mtime_str = ""
                    if mtime_val:
                        try:
                            mtime_str = datetime.fromtimestamp(mtime_val).strftime("%Y-%m-%d %H:%M")
                        except Exception:
                            mtime_str = str(mtime_val)
                    is_dir = _sftp_stat.S_ISDIR(mode) if mode else False
                    is_link = _sftp_stat.S_ISLNK(mode) if mode else False
                    target = ""
                    if is_link:
                        try:
                            target = await sftp.readlink(posixpath.join(path, name))
                        except Exception:
                            target = ""
                    items.append({
                        "name": name,
                        "size": size,
                        "mtime": mtime_str,
                        "mode": _format_mode(mode) if mode else "----------",
                        "mode_num": mode,
                        "is_dir": is_dir,
                        "is_link": is_link,
                        "target": target,
                    })
                items.sort(key=lambda x: (not x["is_dir"], x["name"].lower()))
                return {"items": items, "path": path}
    except asyncssh.PermissionDenied as e:
        raise HTTPException(403, detail=t("sftp.permission_denied", error=e))
    except asyncssh.SFTPNoSuchFile:
        raise HTTPException(404, detail=t("sftp.path_not_found", path=path))
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.error", error=e))


@router.post("/sftp-client/cwd")
async def sftp_client_cwd(req: SftpClientReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                cwd = await sftp.getcwd()
                if not cwd:
                    cwd = "/"
                return {"cwd": cwd}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.error", error=e))


@router.post("/sftp-client/stat")
async def sftp_client_stat(req: SftpClientReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                st = await sftp.stat(req.path)
                mode = st.permissions if hasattr(st, "permissions") else 0
                return {
                    "size": st.size if hasattr(st, "size") else 0,
                    "mtime": st.mtime if hasattr(st, "mtime") else 0,
                    "mode": _format_mode(mode) if mode else "----------",
                    "is_dir": _sftp_stat.S_ISDIR(mode) if mode else False,
                }
    except asyncssh.SFTPNoSuchFile:
        raise HTTPException(404, detail=t("sftp.path_not_found", path=req.path))
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.error", error=e))


@router.post("/sftp-client/read")
async def sftp_client_read(req: SftpClientReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                st = await sftp.stat(req.path)
                size = st.size if hasattr(st, "size") else 0
                if size > 10 * 1024 * 1024:
                    raise HTTPException(413, detail=t("sftp.file_too_large"))
                async with sftp.open(req.path, "r") as f:
                    content = await f.read()
                import base64
                return {"content": base64.b64encode(content.encode("utf-8", errors="replace")).decode(), "encoding": "base64"}
    except asyncssh.SFTPNoSuchFile:
        raise HTTPException(404, detail=t("sftp.file_not_found", path=req.path))
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.error", error=e))


@router.post("/sftp-client/write")
async def sftp_client_write(req: SftpWriteReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                async with sftp.open(req.path, "w") as f:
                    await f.write(req.content)
                return {"ok": True}
    except asyncssh.SFTPNoSuchFile:
        raise HTTPException(404, detail=t("sftp.file_not_found", path=req.path))
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.error", error=e))


@router.post("/sftp-client/download")
async def sftp_client_download(req: SftpClientReq, user: dict = Depends(get_current_user), db: AsyncSession = Depends(get_db)):
    # Look up password from stored connections if not provided
    if not req.password and not req.key_path:
        user_ssh = await _read_user_ssh_db(db, user["id"])
        stored = next((c for c in user_ssh if c.get("id") == req.host or c.get("host") == req.host), None)
        if stored:
            req = SftpClientReq(
                host=req.host, port=req.port, username=req.username or stored.get("username", "root"),
                auth_type=req.auth_type or stored.get("auth_type", "password"),
                password=req.password or stored.get("password", ""),
                key_path=req.key_path or stored.get("key_path", ""),
                path=req.path
            )
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        try:
            conn = await asyncio.wait_for(asyncssh.connect(**kw), timeout=SFTP_CONNECT_TIMEOUT)
        except TimeoutError as e:
            raise TimeoutError(t("sftp.connect_timeout")) from e
        sftp_ctx = conn.start_sftp_client()
        sftp = await sftp_ctx.__aenter__()
        filename = posixpath.basename(req.path)

        async def stream():
            try:
                async with sftp.open(req.path, "rb") as f:
                    while True:
                        chunk = await f.read(65536)
                        if not chunk:
                            break
                        yield chunk
            finally:
                await sftp_ctx.__aexit__(None, None, None)
                conn.close()
                await conn.wait_closed()

        return StreamingResponse(
            stream(),
            media_type="application/octet-stream",
            headers={"Content-Disposition": f'attachment; filename="{filename}"'},
        )
    except asyncssh.SFTPNoSuchFile:
        raise HTTPException(404, detail=t("sftp.file_not_found", path=req.path))
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.error", error=e))


@router.post("/sftp-client/upload")
async def sftp_client_upload(
    user: dict = Depends(get_current_user),
    id: str = "",
    host: str = "",
    port: int = 22,
    username: str = "root",
    auth_type: str = "password",
    password: str = "",
    key_path: str = "",
    path: str = "/",
    file: UploadFile = File(...),
):
    kw = _sftp_conn_kwargs({"id": id, "host": host, "port": port, "username": username, "auth_type": auth_type, "password": password, "key_path": key_path})
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                target = posixpath.join(path, file.filename)
                content = await file.read()
                async with sftp.open(target, "wb") as f:
                    await f.write(content)
                return {"ok": True, "path": target}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.upload_error", error=e))


@router.post("/sftp-client/mkdir")
async def sftp_client_mkdir(req: SftpMkdirReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                await sftp.mkdir(req.path)
                return {"ok": True}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.mkdir_failed", error=e))


@router.post("/sftp-client/touch")
async def sftp_client_touch(req: SftpMkdirReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                try:
                    async with sftp.open(req.path, "a") as f:
                        pass
                except Exception:
                    async with sftp.open(req.path, "w") as f:
                        pass
                return {"ok": True}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.touch_failed", error=e))


@router.post("/sftp-client/rename")
async def sftp_client_rename(req: SftpRenameReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                parent = posixpath.dirname(req.old_path)
                new_path = posixpath.join(parent, req.new_name)
                await sftp.rename(req.old_path, new_path)
                return {"ok": True}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.rename_failed", error=e))


@router.post("/sftp-client/delete")
async def sftp_client_delete(req: SftpMkdirReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                st = await sftp.stat(req.path)
                mode = st.permissions if hasattr(st, "permissions") else 0
                if _sftp_stat.S_ISDIR(mode):
                    await sftp.rmdir(req.path)
                else:
                    await sftp.remove(req.path)
                return {"ok": True}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.delete_failed", error=e))


@router.post("/sftp-client/rmdir")
async def sftp_client_rmdir(req: SftpMkdirReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                await sftp.rmtree(req.path)
                return {"ok": True}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.rmdir_failed", error=e))


@router.post("/sftp-client/chmod")
async def sftp_client_chmod(req: SftpChmodReq, user: dict = Depends(get_current_user)):
    kw = _sftp_conn_kwargs(req.model_dump())
    try:
        async with _sftp_connect(kw) as conn:
            async with conn.start_sftp_client() as sftp:
                await sftp.chmod(req.path, req.mode)
                return {"ok": True}
    except Exception as e:
        raise HTTPException(500, detail=t("sftp.chmod_failed", error=e))


# ═══════════════════════════════════════════════════════════
#  日志 API
# ═══════════════════════════════════════════════════════════


class LogReq(BaseModel):
    level: str = "info"
    msg: str = ""
    module: str = ""
    # 兼容前端上报字段 (api.frontendLog 发的是 message/url)
    message: str = ""
    url: str = ""


@router.post("/log")
def log_receive(req: LogReq):
    # S4: 保持开放(登录前异常也要能上报), 但对写入做长度截断, 防刷与防超长行
    _msg = (req.msg or req.message or "")[:2000]
    _mod = (req.module or req.url or "")[:200]
    _logger.info("Frontend [%s] %s: %s", _mod, (req.level or "info")[:16], _msg)
    return {"ok": True}


@router.get("/logs")
def log_list(user: dict = Depends(require_permission("system:admin"))):
    backend_log = LOGS_DIR / "backend.log"
    frontend_log = LOGS_DIR / "frontend.log"
    lines = []
    for lf in [backend_log, frontend_log]:
        if lf.is_file():
            try:
                content = lf.read_text(encoding="utf-8", errors="replace")
                for line in content.strip().splitlines()[-200:]:
                    lines.append(line)
            except Exception:
                pass
    return {"lines": lines[-400:]}

import time

# ═══════════════════════════════════════════════════════════
#  WebRTC 信令 WebSocket + Agent 管理
# ═══════════════════════════════════════════════════════════

# 在线 wragent 存储
_online_agents: dict[str, dict] = {}  # agent_id -> {ws, info, last_seen}
_online_gateways: dict[str, dict] = {}  # gateway_id -> {ws, name, url, last_seen}
_agent_connections: dict[str, dict] = {}  # room_id -> {browser_ws, agent_id, agent_ws}
_setup_sessions: dict[str, dict] = {}  # sid -> {ws, agent_id, agent_name, created_at}

# ── Agent↔Agent 测速(阶段S): 全局单并发 + 内存历史(deque, 不落库) ──
_speedtest_active: dict | None = None  # {room, user_id, started, last_activity}
_speedtest_history: collections.deque = collections.deque(maxlen=20)
SPEEDTEST_DURATION = 10      # 每方向硬时限(秒)
SPEEDTEST_TTL = 30           # 无进度兜底超时(秒)
SPEEDTEST_LIMITS = (0, 10, 50, 100)  # 限速档: 0=无限制


def _agent_config_with_mirror(config: dict) -> dict:
    """双格式下发: 含 plugins 且无顶层 tunnels 时, 附加合并镜像(旧Agent 2.2.x兼容)"""
    if not isinstance(config, dict) or "plugins" not in config or "tunnels" in config:
        return config
    merged = []
    plugins = config.get("plugins") or {}
    for key in ("tunnel", "socks5"):
        bucket = plugins.get(key)
        if isinstance(bucket, dict):
            merged.extend(bucket.get("tunnels") or [])
    out = dict(config)
    out["tunnels"] = merged
    return out


# ── P1: 后台 ICE 扫描通道（方案 §4.2 ice_scan_req / network_info）──
# 后台 → md(HTTP) → Agent(WS ice_scan_req) → md(WS network_info) → 回写 config_json.net_info
_ice_scan_waiters: dict[str, "asyncio.Future[dict]"] = {}  # agent_id -> pending report
ICE_SCAN_TIMEOUT = 5.0  # 方案 §4.2: 超时 409


async def _persist_net_info(agent_id: str, net_info: dict) -> None:
    """把 Agent 上报的 network_info 合并进 agents.config_json.net_info（只增不覆盖其它键）"""
    try:
        async with get_db_session() as db:
            res = await db.execute(select(Agent).where(Agent.id == agent_id))
            row = res.scalar_one_or_none()
            if not row:
                return
            cfg = {}
            if row.config_json:
                try:
                    cfg = json.loads(row.config_json)
                except Exception:
                    cfg = {}
            if not isinstance(cfg, dict):
                cfg = {}
            cfg["net_info"] = net_info
            await db.execute(
                update(Agent).where(Agent.id == agent_id)
                .values(config_json=json.dumps(cfg, ensure_ascii=False))
            )
            await db.commit()
    except Exception as e:
        _logger.warning("persist net_info failed agent=%s: %s", agent_id, e)


async def _agent_ice_policy(agent_id: str) -> dict:
    """P2 §4.5: 取 agent config_json.ice 的阶梯策略子集, 随 connect_success 下发给网关。
    缺省 true/true(与 IceOptReq 字段一致), 读不到也返回缺省值, 网关侧再有 env 兜底。"""
    _policy = {"auto_fallback": True, "path_cache": True}
    if not agent_id:
        return _policy
    try:
        async with get_db_session() as db:
            res = await db.execute(select(Agent.config_json).where(Agent.id == agent_id))
            raw = res.scalar_one_or_none()
    except Exception as e:
        _logger.warning("read ice policy failed agent=%s: %s", agent_id, e)
        return _policy
    try:
        cfg = json.loads(raw) if raw else {}
        ice = cfg.get("ice") if isinstance(cfg, dict) else None
        if isinstance(ice, dict):
            _policy["auto_fallback"] = bool(ice.get("auto_fallback", True))
            _policy["path_cache"] = bool(ice.get("path_cache", True))
    except Exception:
        pass
    return _policy


async def request_ice_scan(agent_id: str, timeout: float = ICE_SCAN_TIMEOUT) -> dict:
    """下发 ice_scan_req 并等待 network_info 回包。
    Agent 离线 / 5s 超时 → 409（后台"立即扫描"按钮的失败路径）。"""
    if agent_id not in _online_agents:
        raise HTTPException(status_code=409, detail=t("admin.ice_scan_offline"))
    loop = asyncio.get_running_loop()
    fut: asyncio.Future = loop.create_future()
    old = _ice_scan_waiters.pop(agent_id, None)
    if old is not None and not old.done():
        old.cancel()
    _ice_scan_waiters[agent_id] = fut
    try:
        await _online_agents[agent_id]["ws"].send_text(
            json.dumps({"type": "ice_scan_req", "data": {}}))
        return await asyncio.wait_for(fut, timeout)
    except asyncio.TimeoutError:
        raise HTTPException(status_code=409, detail=t("admin.ice_scan_timeout"))
    except asyncio.CancelledError:
        raise HTTPException(status_code=409, detail=t("admin.ice_scan_superseded"))
    finally:
        if _ice_scan_waiters.get(agent_id) is fut:
            del _ice_scan_waiters[agent_id]


async def _handle_network_info(agent_id: str, payload) -> None:
    """Agent→Server: network_info。先落库，再唤醒挂起的扫描请求。"""
    if isinstance(payload, str):
        try:
            payload = json.loads(payload)
        except Exception:
            payload = None
    if not isinstance(payload, dict) or not payload:
        _logger.warning("[AS-WS] network_info invalid from agent=%s", agent_id)
        return
    await _persist_net_info(agent_id, payload)
    _logger.info("[AS-WS] network_info agent=%s if_hash=%s interfaces=%d",
                 agent_id, payload.get("if_hash", ""),
                 len(payload.get("interfaces") or []))
    fut = _ice_scan_waiters.pop(agent_id, None)
    if fut is not None and not fut.done():
        fut.set_result(payload)


async def _push_config_to_agent(agent_id: str, config: dict) -> bool:
    import json
    if agent_id not in _online_agents:
        return False
    try:
        ws = _online_agents[agent_id]["ws"]
        msg = json.dumps({"type": "config_update", "data": _agent_config_with_mirror(config)})
        await ws.send_text(msg)
        return True
    except Exception:
        return False

async def push_upgrade_to_agent(agent_id: str, version: str, download_url: str) -> bool:
    """通过WebSocket推送upgrade指令到在线agent"""
    if agent_id not in _online_agents:
        return False
    ws = _online_agents[agent_id]["ws"]
    try:
        import json as _json
        await ws.send_text(_json.dumps({
            "type": "upgrade",
            "data": {"version": version, "download_url": download_url},
        }))
        return True
    except Exception:
        return False


async def push_upgrade_to_gateway(gateway_id: str, version: str, download_url: str) -> bool:
    """通过WebSocket推送upgrade指令到在线gateway"""
    if gateway_id not in _online_gateways:
        return False
    ws = _online_gateways[gateway_id]["ws"]
    try:
        import json as _json
        await ws.send_text(_json.dumps({
            "type": "upgrade",
            "data": {"version": version, "download_url": download_url},
        }))
        return True
    except Exception:
        return False


async def _user_sees_agent(user_id: str, agent_id: str) -> bool:
    """与 adminListAgents 相同的可见性过滤(owner/共享)"""
    try:
        async with get_db_session() as db:
            r = await db.execute(
                select(Agent.id).where(
                    Agent.id == agent_id,
                    (Agent.owner_id == user_id)
                    | (Agent.shared_with == "all")
                    | Agent.shared_with.contains(f'"{user_id}"'),
                )
            )
            return r.scalar_one_or_none() is not None
    except Exception as e:
        _logger.warning("[SPEEDTEST] visibility check failed: %s", e)
        return False


async def _user_sees_gateway(user_id: str, gateway_id: str) -> bool:
    """与 adminListGateways 相同的可见性过滤(owner/共享)"""
    try:
        async with get_db_session() as db:
            r = await db.execute(
                select(Gateway.id).where(
                    Gateway.id == gateway_id,
                    (Gateway.owner_id == user_id)
                    | (Gateway.shared_with == "all")
                    | Gateway.shared_with.contains(f'"{user_id}"'),
                )
            )
            return r.scalar_one_or_none() is not None
    except Exception as e:
        _logger.warning("[ISOLATE] gateway visibility check failed: %s", e)
        return False


async def _visible_agent_ids(user_id: str) -> set:
    """当前用户可见的 Agent id 集合(一条 SQL, 供列表接口过滤)"""
    try:
        async with get_db_session() as db:
            r = await db.execute(
                select(Agent.id).where(
                    (Agent.owner_id == user_id)
                    | (Agent.shared_with == "all")
                    | Agent.shared_with.contains(f'"{user_id}"')
                )
            )
            return {row[0] for row in r.all()}
    except Exception as e:
        _logger.warning("[ISOLATE] visible agents query failed: %s", e)
        return set()


async def _visible_gateway_ids(user_id: str) -> set:
    """当前用户可见的 Gateway id 集合(一条 SQL, 供列表接口过滤)"""
    try:
        async with get_db_session() as db:
            r = await db.execute(
                select(Gateway.id).where(
                    (Gateway.owner_id == user_id)
                    | (Gateway.shared_with == "all")
                    | Gateway.shared_with.contains(f'"{user_id}"')
                )
            )
            return {row[0] for row in r.all()}
    except Exception as e:
        _logger.warning("[ISOLATE] visible gateways query failed: %s", e)
        return set()


async def _has_active_turn() -> bool:
    """relay 强制: 存在启用的 coturn 才允许测速(TC-ST05)"""
    try:
        async with get_db_session() as db:
            r = await db.execute(select(CoturnServer).where(CoturnServer.is_active == True).limit(1))
            return r.scalar_one_or_none() is not None
    except Exception as e:
        _logger.warning("[SPEEDTEST] coturn check failed: %s", e)
        return False


async def _speedtest_end(room_id: str, reason: str = "", notify_agents: bool = True) -> None:
    """结束测速会话: 释放全局锁/清房间/通知两端与浏览器(幂等)"""
    global _speedtest_active
    conn = _agent_connections.pop(room_id, None)
    if _speedtest_active and _speedtest_active.get("room") == room_id:
        _task = _speedtest_active.get("task")
        if _task is not None:
            _task.cancel()
        _speedtest_active = None
    if not conn:
        return
    if notify_agents:
        # browser_ws=源Agent, agent_ws=目标Agent
        for w in (conn.get("browser_ws"), conn.get("agent_ws")):
            if w is None:
                continue
            try:
                await w.send_text(json.dumps({"type": "speedtest_stop", "room_id": room_id, "detail": reason}))
            except Exception:
                pass
    if reason and conn.get("progress_ws") is not None:
        try:
            await conn["progress_ws"].send_text(json.dumps({
                "type": "speedtest_error", "room_id": room_id, "detail": reason,
            }))
        except Exception:
            pass
    _logger.info("[SPEEDTEST] session ended room=%s reason=%s", room_id, reason or "result")


async def _speedtest_watchdog(room_id: str) -> None:
    """30s TTL 兜底: 无进度即超时; 会话总时长上限 60s"""
    while True:
        await asyncio.sleep(5)
        if room_id not in _agent_connections:
            return
        act = _speedtest_active
        if not act or act.get("room") != room_id:
            return
        now = time.time()
        if now - act["last_activity"] > SPEEDTEST_TTL or now - act["started"] > 60:
            await _speedtest_end(room_id, reason=t("ws.speedtest_timeout"))
            return


def _speedtest_data(msg: dict) -> dict:
    """提取 agent 上报的 payload(data 为 str/dict 兼容)"""
    data = msg.get("data")
    if isinstance(data, str):
        try:
            data = json.loads(data)
        except Exception:
            data = {}
    return data if isinstance(data, dict) else {}


@router.websocket("/ws/webrtc")
async def ws_webrtc(ws: WebSocket):
    """WebRTC 信令服务器 - 连接 wragent 和浏览器"""
    global _speedtest_active
    await ws.accept()
    role = ""  # "agent" or "browser"
    agent_id = ""
    user_id = ""
    _role_locked = False  # gateway/agent 注册后禁止被 connect_* 降级为 browser
    try:
        while True:
            data = await ws.receive_text()
            # R11: 坏 JSON 跳过不断连
            try:
                msg = json.loads(data)
            except (json.JSONDecodeError, TypeError):
                _logger.warning("[AS-WS] invalid JSON from %s, skipped", ws.client.host if ws.client else "-")
                continue
            if not isinstance(msg, dict):
                continue
            msg_type = msg.get("type", "")

            if msg_type == "register":
                # wragent 注册 (验证 token) — S5: 必须已有 DB 记录且 token 匹配
                agent_id = msg.get("agent_id", "")
                agent_name = msg.get("agent_name", agent_id)
                client_ip = ws.headers.get("x-real-ip") or (ws.client.host if ws.client else "-")
                # token 在 msg.data (嵌套) 中, 兼容顶层
                _data_field = msg.get("data", {})
                if isinstance(_data_field, str):
                    try:
                        _data_field = json.loads(_data_field)
                    except Exception:
                        _data_field = {}
                agent_token = (msg.get("token") if msg.get("token") else
                               _data_field.get("token", "") if isinstance(_data_field, dict) else "")

                if not agent_id or not agent_token:
                    await ws.send_text(json.dumps({
                        "type": "register_failed",
                        "detail": t("ws.auth_failed")
                    }))
                    continue
                db_token = await _get_agent_token_from_db(agent_id)
                if not db_token or agent_token != db_token:
                    _logger.warning("webrtc: agent %s token mismatch/rejected (agent_token=%r db_token=%r)",
                                    agent_id, agent_token[:16] if agent_token else "",
                                    db_token[:16] if db_token else "")
                    await ws.send_text(json.dumps({
                        "type": "register_failed",
                        "detail": t("ws.token_expired_agent")
                    }))
                    continue

                role = "agent"
                _role_locked = True
                _db_ct = ""
                try:
                    async with get_db_session() as _db:
                        _r = await _db.execute(select(Agent.conn_type).where(Agent.id == agent_id))
                        _db_ct = _r.scalar_one_or_none() or ""
                except Exception:
                    pass
                _agent_deploy = _data_field.get("deploy_mode", "") if isinstance(_data_field, dict) else ""
                _agent_arch = _data_field.get("arch", "") if isinstance(_data_field, dict) else ""
                _online_agents[agent_id] = {
                    "ws": ws,
                    "name": agent_name,
                    "version": msg.get("agent_version", _data_field.get("agent_version", "1.0.0")) if isinstance(_data_field, dict) else msg.get("agent_version", "1.0.0"),
                    "last_seen": time.time(),
                    "status": "online",
                    "ip": client_ip,
                    "conn_type": _db_ct,
                    "deploy_mode": _agent_deploy,
                    "arch": _agent_arch or "amd64",
                }
                _agent_version = msg.get("agent_version", _data_field.get("agent_version", "1.0.0")) if isinstance(_data_field, dict) else msg.get("agent_version", "1.0.0")
                await _upsert_agent_to_db(agent_id, agent_name, _agent_version, _agent_deploy)
                _logger.info("[AS-WS] agent registered %s (%s) v%s from %s", agent_id, agent_name, _agent_version, client_ip)

                # 获取默认 coturn 服务器的 ICE 配置下发给 agent
                _ice_servers = []
                try:
                    async with get_db_session() as _db:
                        _result = await _db.execute(select(CoturnServer).where(CoturnServer.is_active == True).order_by(CoturnServer.is_default.desc()).limit(1))
                        _coturn = _result.scalar_one_or_none()
                        if _coturn:
                            _ice_servers = get_ice_servers(agent_id, _coturn.host, _coturn.port, _coturn.tls_port, _coturn.secret)
                except Exception as _e:
                    _logger.warning("[AS-WS] failed to get ICE servers for agent %s: %s", agent_id, _e)

                # Fetch agent config from DB
                _agent_config = {}
                try:
                    async with get_db_session() as _db2:
                        _agent_result = await _db2.execute(select(Agent).where(Agent.id == agent_id))
                        _agent_row = _agent_result.scalar_one_or_none()
                        if _agent_row and _agent_row.config_json:
                            _agent_config = json.loads(_agent_row.config_json)
                except Exception:
                    pass

                _needs_upgrade, _latest_version = _check_version_upgrade("agent", _agent_version)
                await ws.send_text(json.dumps({
                    "type": "register_success",
                    "agent_id": agent_id,
                    "ice_servers": _ice_servers,
                    "config": _agent_config_with_mirror(_agent_config),
                    "upgrade": _needs_upgrade,
                    "latest_version": _latest_version,
                }))

            elif msg_type == "register_gateway":
                # wrgateway 注册 (验证 token) — S5: 必须已有 DB 记录且 token 匹配
                gw_id = msg.get("gateway_id", "")
                gw_name = msg.get("gateway_name", "")
                client_ip = ws.headers.get("x-real-ip") or (ws.client.host if ws.client else "-")
                gw_token = msg.get("token", "")

                if not gw_id or not gw_token:
                    await ws.send_text(json.dumps({
                        "type": "register_failed",
                        "detail": t("ws.auth_failed")
                    }))
                    continue
                db_token = await _get_gateway_token_from_db(gw_id)
                if not db_token or gw_token != db_token:
                    _logger.warning("webrtc: gateway %s token mismatch/rejected", gw_id)
                    await ws.send_text(json.dumps({
                        "type": "register_failed",
                        "detail": t("ws.token_expired_gateway")
                    }))
                    continue

                role = "gateway"
                _role_locked = True
                # R1: heartbeat/finally 使用 agent_id 字段，gateway 注册时写入 gw_id
                agent_id = gw_id
                _gw_version = msg.get("version", "1.0.0")
                _online_gateways[gw_id] = {
                    "ws": ws,
                    "name": gw_name or gw_id,
                    "version": _gw_version,
                    "last_seen": time.time(),
                    "status": "online",
                    "ip": client_ip,
                }
                await _upsert_gateway_to_db(gw_id, gw_name, _gw_version)
                _logger.info("[GS-WS] gateway registered %s (%s) v%s from %s", gw_id, gw_name or gw_id, _gw_version, client_ip)
                # 获取 coturn 服务器的 ICE 配置下发给 gateway
                _ice_servers = []
                try:
                    async with get_db_session() as _db:
                        _result = await _db.execute(select(CoturnServer).where(CoturnServer.is_active == True).order_by(CoturnServer.is_default.desc()).limit(1))
                        _coturn = _result.scalar_one_or_none()
                        if _coturn:
                            _ice_servers = get_ice_servers(gw_id, _coturn.host, _coturn.port, _coturn.tls_port, _coturn.secret)
                except Exception as _e:
                    _logger.warning("[GS-WS] failed to get ICE servers for gateway %s: %s", gw_id, _e)
                _needs_upgrade, _latest_version = _check_version_upgrade("gateway", _gw_version)
                await ws.send_text(json.dumps({
                    "type": "register_success",
                    "gateway_id": gw_id,
                    "ice_servers": _ice_servers,
                    "upgrade": _needs_upgrade,
                    "latest_version": _latest_version,
                }))

            elif msg_type == "setup_start":
                # wragent 请求 Tailscale 风格认证
                agent_id = msg.get("agent_id", "")
                agent_name = msg.get("agent_name", agent_id)
                sid = uuid.uuid4().hex[:16]
                _setup_sessions[sid] = {
                    "ws": ws,
                    "agent_id": agent_id,
                    "agent_name": agent_name,
                    "created_at": time.time(),
                }
                _logger.info("webrtc: setup_start agent=%s sid=%s", agent_id, sid)
                base_url = os.environ.get("BASE_URL") or os.environ.get("PUBLIC_URL") or ""
                if not base_url:
                    xproto = ws.headers.get("x-forwarded-proto", "") or "https"
                    xhost = (ws.headers.get("x-forwarded-host", "")
                             or ws.headers.get("host", "") or "")
                    if xhost:
                        base_url = f"{xproto}://{xhost}"
                if not base_url:
                    base_url = "https://domain:5588"
                base_url = base_url.rstrip("/")
                await ws.send_text(json.dumps({
                    "type": "setup_url",
                    "agent_id": agent_id,
                    "data": {"url": f"{base_url}/agent/s/{sid}", "sid": sid},
                }))

            elif msg_type == "connect_agent":
                # 浏览器请求连接到 wragent — S4: token 必选
                target_agent_id = msg.get("agent_id", "")
                room_id = msg.get("room_id", f"room_{target_agent_id}_{int(time.time())}")
                token = msg.get("token", "")
                client_ip = ws.headers.get("x-real-ip") or (ws.client.host if ws.client else "-")

                if not token:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.auth_failed")}))
                    continue
                try:
                    from .auth import _decode_token
                    payload = _decode_token(token)
                    user_id = payload.get("user_id", "")
                except Exception:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.auth_failed")}))
                    continue

                if target_agent_id not in _online_agents:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.agent_offline")}))
                    continue

                # S1(隔离): 可见性与 /api/admin/agents 同规则, 不可见不得建房开 shell
                if not (await _user_sees_agent(user_id, target_agent_id)):
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.agent_no_permission")}))
                    continue

                # R3: room 属主 — 同一 browser 可重连覆盖，他人不得劫持
                _dup_same_ws = False
                if room_id in _agent_connections:
                    existing = _agent_connections[room_id]
                    if existing.get("browser_ws") is ws:
                        # 同 ws 重复 connect_agent（hub 补发）：幂等回成功，不重发 browser_connect
                        # 否则前端会二次 createPeer，agent 同 room 关旧 Peer 杀掉 SSH
                        _dup_same_ws = True
                    elif existing.get("browser_ws") is not None:
                        room_id = f"{room_id}_{uuid.uuid4().hex[:8]}"

                if _dup_same_ws:
                    await ws.send_text(json.dumps({
                        "type": "connect_success",
                        "room_id": room_id,
                        "agent_id": target_agent_id,
                        "client_ip": client_ip,
                        "agent_ip": _online_agents[target_agent_id].get("ip", ""),
                        "existing": True,
                    }))
                    _logger.info("[BS-WS] duplicate connect_agent ignored (same ws), room %s", room_id)
                    continue

                if not _role_locked:
                    role = "browser"
                agent_ws = _online_agents[target_agent_id]["ws"]
                _agent_connections[room_id] = {
                    "browser_ws": ws,
                    "agent_id": target_agent_id,
                    "agent_ws": agent_ws,
                    "user_id": user_id,
                    "created_at": time.time(),
                }

                # 通知 agent 有浏览器连接
                await agent_ws.send_text(json.dumps({
                    "type": "browser_connect",
                    "room_id": room_id,
                    "user_id": user_id,
                }))

                await ws.send_text(json.dumps({
                    "type": "connect_success",
                    "room_id": room_id,
                    "agent_id": target_agent_id,
                    "client_ip": client_ip,
                    "agent_ip": _online_agents[target_agent_id].get("ip", ""),
                }))
                _logger.info("[BS-WS] browser connected to agent %s, room %s", target_agent_id, room_id)

            elif msg_type == "connect_gateway":
                # 浏览器请求通过网关连接 — S4: token 必选
                target_gateway_id = msg.get("gateway_id", msg.get("agent_id", ""))
                target_agent_id = msg.get("agent_id", target_gateway_id)
                # 如果同时传了 gateway_id，则 agent_id 就是真正的 agent
                if msg.get("gateway_id"):
                    target_agent_id = msg.get("agent_id", "")
                room_id = msg.get("room_id", f"room_gw_{target_gateway_id}_{int(time.time())}")
                token = msg.get("token", "")
                client_ip = ws.headers.get("x-real-ip") or (ws.client.host if ws.client else "-")

                if not token:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.auth_failed")}))
                    continue
                try:
                    from .auth import _decode_token
                    payload = _decode_token(token)
                    user_id = payload.get("user_id", "")
                except Exception:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.auth_failed")}))
                    continue

                if target_gateway_id not in _online_gateways:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.gateway_offline")}))
                    continue

                # S1(隔离): 网关与目标 Agent 可见性, 同 /api/admin/gateways、/api/admin/agents 规则
                if not (await _user_sees_gateway(user_id, target_gateway_id)):
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.gateway_no_permission")}))
                    continue
                if target_agent_id and target_agent_id != target_gateway_id \
                        and not (await _user_sees_agent(user_id, target_agent_id)):
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.agent_no_permission")}))
                    continue

                # R3: room 属主
                if room_id in _agent_connections:
                    existing = _agent_connections[room_id]
                    if existing.get("browser_ws") is not None and existing.get("browser_ws") is not ws:
                        room_id = f"{room_id}_{uuid.uuid4().hex[:8]}"

                if not _role_locked:
                    role = "browser"
                gw_ws = _online_gateways[target_gateway_id]["ws"]

                # 查找实际 agent WS (offer/answer/candidate 通过 agent 路由)
                agent_ws = None
                if target_agent_id and target_agent_id in _online_agents:
                    agent_ws = _online_agents[target_agent_id]["ws"]

                _agent_connections[room_id] = {
                    "browser_ws": ws,
                    "agent_id": target_agent_id or target_gateway_id,
                    "agent_ws": agent_ws,
                    "gateway_ws": gw_ws,
                    "user_id": user_id,
                    "created_at": time.time(),
                }

                # 通知网关有浏览器连接
                await gw_ws.send_text(json.dumps({
                    "type": "browser_connect",
                    "room_id": room_id,
                    "user_id": user_id,
                }))

                # 通知 Agent 有浏览器连接（让 Agent 准备接收 WebRTC offer）
                if agent_ws:
                    await agent_ws.send_text(json.dumps({
                        "type": "browser_connect",
                        "room_id": room_id,
                        "user_id": user_id,
                    }))

                # P2: 网关阶梯所需的策略(config_json.ice.auto_fallback/path_cache), 网关按 agent 记忆
                await ws.send_text(json.dumps({
                    "type": "connect_success",
                    "room_id": room_id,
                    "agent_id": target_agent_id or target_gateway_id,
                    "client_ip": client_ip,
                    "agent_ip": _online_agents.get(target_agent_id, {}).get("ip", "") if target_agent_id else "",
                    "gateway_ip": _online_gateways.get(target_gateway_id, {}).get("ip", "") if target_gateway_id else "",
                    "ice_policy": await _agent_ice_policy(target_agent_id or target_gateway_id),
                }))
                _logger.info("[BS-WS] browser connected to gateway %s agent %s, room %s (agent_online=%s)",
                    target_gateway_id, target_agent_id, room_id, bool(agent_ws))

            elif msg_type == "connect_tunnel":
                # 隧道连接请求（agent → agent）
                _data = msg.get("data", {})
                if isinstance(_data, str):
                    import json as _json
                    try: _data = _json.loads(_data)
                    except: _data = {}
                agent_id = msg.get("agent_id", "")
                target_agent_id = _data.get("target_agent_id", "") or msg.get("target_agent_id", "")
                token = _data.get("token", "") or msg.get("token", "")
                _logger.info("[TUNNEL] connect_tunnel: agent=%s target=%s", agent_id, target_agent_id)

                # agent token 认证
                db_token = await _get_agent_token_from_db(agent_id)
                if not db_token or db_token != token:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.auth_failed")}))
                    continue
                if target_agent_id not in _online_agents:
                    await ws.send_text(json.dumps({"type": "error", "detail": t("ws.target_agent_offline")}))
                    continue

                # 复用浏览器路由逻辑；不改写 role——发起方是 agent 自己，
                # 改成 browser 会打断同连接上的 heartbeat last_seen 刷新（假活根因）
                room_id = f"tunnel_{agent_id}_{target_agent_id}_{int(time.time())}"
                agent_ws = _online_agents[target_agent_id]["ws"]
                _agent_connections[room_id] = {
                    "browser_ws": ws,
                    "agent_id": target_agent_id,
                    "agent_ws": agent_ws,
                    "user_id": f"tunnel:{agent_id}",
                    "created_at": time.time(),
                }
                await agent_ws.send_text(json.dumps({"type": "browser_connect", "room_id": room_id}))
                await ws.send_text(json.dumps({"type": "connect_success", "room_id": room_id, "agent_id": target_agent_id}))
                _logger.info("[TUNNEL] tunnel connected: agent=%s target=%s room=%s", agent_id, target_agent_id, room_id)

            elif msg_type == "speedtest_start":
                # 浏览器发起 Agent↔Agent 测速(阶段S): 校验→建房→通知两端
                token = msg.get("token", "")
                if not token:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.auth_failed")}))
                    continue
                try:
                    from .auth import _decode_token
                    _payload = _decode_token(token)
                    _st_user = _payload.get("user_id", "")
                except Exception:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.auth_failed")}))
                    continue

                _src = msg.get("source", "")
                _dst = msg.get("target", "")
                try:
                    _limit = int(msg.get("mbps_limit", 0) or 0)
                except (TypeError, ValueError):
                    _limit = -1
                if not _src or not _dst or _src == _dst:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.speedtest_same_agent")}))
                    continue
                if _limit not in SPEEDTEST_LIMITS:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.speedtest_invalid_limit")}))
                    continue
                # TTL 兜底: 先清陈旧会话, 再单并发判定(TC-ST03)
                if _speedtest_active and time.time() - _speedtest_active["last_activity"] > SPEEDTEST_TTL:
                    await _speedtest_end(_speedtest_active["room"], reason=t("ws.speedtest_timeout"))
                if _speedtest_active is not None:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.speedtest_busy")}))
                    continue
                if _src not in _online_agents:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.agent_offline")}))
                    continue
                if _dst not in _online_agents:
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.target_agent_offline")}))
                    continue
                if not (await _user_sees_agent(_st_user, _src)) or not (await _user_sees_agent(_st_user, _dst)):
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.speedtest_permission")}))
                    continue
                if not (await _has_active_turn()):
                    await ws.send_text(json.dumps({"type": "speedtest_error", "detail": t("ws.speedtest_need_turn")}))
                    continue

                _room = f"speedtest_{_src}_{_dst}_{int(time.time())}"
                _now = time.time()
                _agent_connections[_room] = {
                    "browser_ws": _online_agents[_src]["ws"],   # SDP offer 源=发起Agent
                    "agent_id": _dst,
                    "agent_ws": _online_agents[_dst]["ws"],
                    "user_id": f"speedtest:{_st_user}",
                    "progress_ws": ws,                          # 结果/进度回传浏览器
                    "speedtest_src": _src,
                    "created_at": _now,
                }
                _speedtest_active = {
                    "room": _room, "user_id": _st_user,
                    "started": _now, "last_activity": _now,
                    "task": asyncio.create_task(_speedtest_watchdog(_room)),
                }
                # data 包装: Agent 端 ws.Message 解析 msg.Data(Go), 顶层字段保留兼容测试/诊断
                await _online_agents[_src]["ws"].send_text(json.dumps({
                    "type": "speedtest_connect", "room_id": _room,
                    "duration": SPEEDTEST_DURATION, "mbps_limit": _limit,
                    "data": {"room_id": _room, "duration": SPEEDTEST_DURATION, "mbps_limit": _limit},
                }))
                await _online_agents[_dst]["ws"].send_text(json.dumps({"type": "browser_connect", "room_id": _room}))
                await ws.send_text(json.dumps({
                    "type": "speedtest_started", "room_id": _room,
                    "source": _src, "target": _dst, "mbps_limit": _limit,
                    "duration": SPEEDTEST_DURATION,
                }))
                _logger.info("[SPEEDTEST] started by=%s src=%s dst=%s limit=%s room=%s",
                              _st_user, _src, _dst, _limit, _room)

            elif msg_type in ("speedtest_progress", "speedtest_result", "speedtest_error"):
                # agent → 浏览器: 进度/结果/错误转发(仅房间成员)
                room_id = msg.get("room_id", "")
                conn = _agent_connections.get(room_id)
                if not conn:
                    continue
                if not (conn.get("browser_ws") is ws or conn.get("agent_ws") is ws):
                    _logger.warning("[SPEEDTEST] %s rejected: room=%s sender not member", msg_type, room_id)
                    continue
                payload = _speedtest_data(msg) or {k: v for k, v in msg.items() if k != "data"}
                if _speedtest_active and _speedtest_active.get("room") == room_id:
                    _speedtest_active["last_activity"] = time.time()
                target_ws = conn.get("progress_ws")
                if msg_type == "speedtest_result":
                    _ping = conn.get("ping") or {}
                    _speedtest_history.append({
                        "source": conn.get("speedtest_src", ""),
                        "target": conn.get("agent_id", ""),
                        "user_id": _speedtest_active["user_id"] if _speedtest_active else "",
                        "up_mbps": payload.get("up_mbps", 0),
                        "down_mbps": payload.get("down_mbps", 0),
                        "duration": payload.get("duration", 0),
                        "ping_ms": _ping.get("ping_ms", 0),
                        "jitter_ms": _ping.get("jitter_ms", 0),
                        "loss_pct": _ping.get("loss_pct", 0),
                        "finished_at": time.time(),
                    })
                if target_ws is not None:
                    try:
                        await target_ws.send_text(json.dumps(payload))
                    except Exception:
                        # 浏览器已断开 → 会话终止
                        await _speedtest_end(room_id, reason=t("ws.speedtest_timeout"), notify_agents=True)
                        continue
                if msg_type in ("speedtest_result", "speedtest_error"):
                    await _speedtest_end(room_id,
                                         reason=payload.get("detail", "") if msg_type == "speedtest_error" else "",
                                         notify_agents=False)

            elif msg_type == "speedtest_ping":
                # 源端上报 PING 结果: 缓存供 result 合并进历史, 并转发浏览器
                _proom = msg.get("room_id", "") or _speedtest_data(msg).get("room_id", "")
                _pconn = _agent_connections.get(_proom)
                if _pconn:
                    _pp = _speedtest_data(msg)
                    _pconn["ping"] = {
                        "ping_ms": _pp.get("ping_ms", 0),
                        "jitter_ms": _pp.get("jitter_ms", 0),
                        "loss_pct": _pp.get("loss_pct", 0),
                    }
                    _ptarget = _pconn.get("progress_ws")
                    if _ptarget is not None:
                        try:
                            await _ptarget.send_text(json.dumps({
                                "type": "speedtest_ping",
                                "ping_ms": _pp.get("ping_ms", 0),
                                "jitter_ms": _pp.get("jitter_ms", 0),
                                "loss_pct": _pp.get("loss_pct", 0),
                            }))
                        except Exception:
                            pass
                continue

            elif msg_type == "speedtest_cancel":
                # 浏览器中止: 双端 stop + 清房间(TC-ST02)
                room_id = msg.get("room_id", "")
                conn = _agent_connections.get(room_id)
                if not conn or conn.get("progress_ws") is not ws:
                    continue
                await _speedtest_end(room_id, reason="", notify_agents=True)
                try:
                    await ws.send_text(json.dumps({"type": "speedtest_cancelled", "room_id": room_id}))
                except Exception:
                    pass

            elif msg_type == "offer":
                # 转发 Offer 到 agent — R3: 仅房间成员(browser/gateway)可发
                room_id = msg.get("room_id", "")
                conn = _agent_connections.get(room_id)
                if not conn:
                    continue
                is_browser = conn.get("browser_ws") is ws
                is_gw = bool(conn.get("gateway_ws")) and conn.get("gateway_ws") is ws
                if not (is_browser or is_gw):
                    _logger.warning("[AS-WS] offer rejected: room=%s not owned by sender", room_id)
                    continue
                if conn.get("agent_ws"):
                    offer_sdp = msg.get("sdp") or msg.get("data")
                    forward = {
                        "type": "offer",
                        "room_id": room_id,
                        "data": offer_sdp,
                        "sdp": offer_sdp,
                        "from": "gateway" if is_gw else "browser",
                    }
                    # P2 §4.5: 网关宣告的 ICE 等级(L1只放行缓存接口/L2规则/L3不过滤), md 重造 dict 必须显式透传
                    _ice_lvl = msg.get("ice_level")
                    if isinstance(_ice_lvl, int) and _ice_lvl > 0:
                        forward["ice_level"] = _ice_lvl
                    await conn["agent_ws"].send_text(json.dumps(forward))

            elif msg_type == "answer":
                # 转发 Answer — R3: agent 的 answer → gateway(若有)否则 browser
                room_id = msg.get("room_id", "")
                conn = _agent_connections.get(room_id)
                # 每次 answer 都打一条会把信令路径塞满磁盘写；降到 DEBUG（默认级别下零开销）
                _logger.debug("[GA-DC] answer routed: room=%s has_conn=%s has_browser=%s", room_id, bool(conn), bool(conn and conn.get("browser_ws")))
                if not conn:
                    continue
                is_agent = conn.get("agent_ws") is ws
                is_gw = bool(conn.get("gateway_ws")) and conn.get("gateway_ws") is ws
                if is_agent:
                    # direct: → browser; gateway 模式: → gateway (browser 无 Peer)
                    target = conn.get("gateway_ws") or conn.get("browser_ws")
                    if target:
                        forward = {
                            "type": "answer",
                            "room_id": room_id,
                            "sdp": msg.get("data") or msg.get("sdp"),
                            "from": "agent",
                        }
                        await target.send_text(json.dumps(forward))
                elif is_gw and conn.get("browser_ws"):
                    # gateway 侧 answer → browser (若有)
                    forward = {
                        "type": "answer",
                        "room_id": room_id,
                        "sdp": msg.get("data") or msg.get("sdp"),
                        "from": "gateway",
                    }
                    await conn["browser_ws"].send_text(json.dumps(forward))
                else:
                    _logger.warning("[AS-WS] answer rejected: room=%s not from agent/gateway", room_id)

            elif msg_type == "candidate":
                # 转发 ICE 候选 — R3: 仅房间成员可发
                room_id = msg.get("room_id", "")
                from_role = msg.get("from", "")
                conn = _agent_connections.get(room_id)
                # ICE candidate 每连接 5–30 条，逐条写盘属事件循环内同步 I/O；降到 DEBUG
                _logger.debug("[GA-DC] candidate routed: room=%s from=%s has_conn=%s has_agent=%s has_browser=%s", room_id, from_role, bool(conn), bool(conn and conn.get("agent_ws")), bool(conn and conn.get("browser_ws")))
                if not conn:
                    continue
                is_browser = conn.get("browser_ws") is ws
                is_agent = conn.get("agent_ws") is ws
                is_gw = conn.get("gateway_ws") is ws if conn.get("gateway_ws") else False
                if not (is_browser or is_agent or is_gw):
                    _logger.warning("[AS-WS] candidate rejected: room=%s sender not member", room_id)
                    continue
                if (from_role == "browser" or is_browser) and conn.get("agent_ws") and not is_agent:
                    _cand = msg.get("candidate") if msg.get("candidate") is not None else msg.get("data")
                    forward = {
                        "type": "candidate",
                        "room_id": room_id,
                        "data": _cand,
                        "candidate": _cand,
                        "from": "browser",
                    }
                    await conn["agent_ws"].send_text(json.dumps(forward))
                elif (from_role in ("agent", "gateway") or is_agent or is_gw) and conn.get("browser_ws") and not is_browser:
                    _cand = msg.get("data") if msg.get("data") is not None else msg.get("candidate")
                    forward = {
                        "type": "candidate",
                        "room_id": room_id,
                        "data": _cand,
                        "candidate": _cand,
                        "from": from_role or ("agent" if is_agent else "gateway"),
                    }
                    await conn["browser_ws"].send_text(json.dumps(forward))

            elif msg_type in ("heartbeat", "ping"):
                # 心跳 — 兼容 gateway 旧版发 ping；R1: gateway 用 agent_id(已写入 gw_id)
                # 假活自愈: 被看门狗踢出在线表后，同一条已鉴权 WS 的心跳可自动回填
                _hb_ok = False
                if role == "agent" and agent_id:
                    _now_ts = time.time()
                    if agent_id in _online_agents:
                        _online_agents[agent_id]["last_seen"] = _now_ts
                    else:
                        _client_ip = ws.headers.get("x-real-ip") or (ws.client.host if ws.client else "-")
                        _rh_name, _rh_ver, _rh_ct = agent_id, "", ""
                        try:
                            async with get_db_session() as _db:
                                _r = await _db.execute(
                                    select(Agent.name, Agent.version, Agent.conn_type).where(Agent.id == agent_id)
                                )
                                _row = _r.first()
                                if _row:
                                    _rh_name = _row[0] or agent_id
                                    _rh_ver = _row[1] or ""
                                    _rh_ct = _row[2] or ""
                        except Exception:
                            pass
                        _online_agents[agent_id] = {
                            "ws": ws,
                            "name": _rh_name,
                            "version": _rh_ver or "unknown",
                            "last_seen": _now_ts,
                            "status": "online",
                            "ip": _client_ip,
                            "conn_type": _rh_ct,
                        }
                        _logger.info("[AS-WS] agent %s rehydrated via heartbeat after stale v=%s", agent_id, _rh_ver or "unknown")
                    _hb_ok = True
                elif role == "gateway" and agent_id:
                    _now_ts = time.time()
                    if agent_id in _online_gateways:
                        _online_gateways[agent_id]["last_seen"] = _now_ts
                    else:
                        _client_ip = ws.headers.get("x-real-ip") or (ws.client.host if ws.client else "-")
                        _rh_gw_name, _rh_gw_ver = agent_id, ""
                        try:
                            async with get_db_session() as _db:
                                _r = await _db.execute(
                                    select(Gateway.name, Gateway.version).where(Gateway.id == agent_id)
                                )
                                _row = _r.first()
                                if _row:
                                    _rh_gw_name = _row[0] or agent_id
                                    _rh_gw_ver = _row[1] or ""
                        except Exception:
                            pass
                        _online_gateways[agent_id] = {
                            "ws": ws,
                            "name": _rh_gw_name,
                            "version": _rh_gw_ver or "unknown",
                            "last_seen": _now_ts,
                            "status": "online",
                            "ip": _client_ip,
                        }
                        _logger.info("[GS-WS] gateway %s rehydrated via heartbeat after stale v=%s", agent_id, _rh_gw_ver or "unknown")
                    _hb_ok = True
                else:
                    # role 被覆盖但仍是本连接注册的 agent/gateway：仍刷新 last_seen，避免看门狗误踢
                    _rescued = False
                    if agent_id and agent_id in _online_agents and _online_agents[agent_id].get("ws") is ws:
                        _online_agents[agent_id]["last_seen"] = time.time()
                        _rescued = True
                    elif agent_id and agent_id in _online_gateways and _online_gateways[agent_id].get("ws") is ws:
                        _online_gateways[agent_id]["last_seen"] = time.time()
                        _rescued = True
                    if not _rescued:
                        _logger.warning("[AS-WS] heartbeat without agent/gateway role role=%r agent_id=%r type=%s",
                                        role, agent_id, msg_type)
                # L2(延迟): 回显客户端 ts, 浏览器用 now-ts 得到信令 RTT(不受时钟偏差影响)
                _ack = {"type": "heartbeat_ack"}
                if isinstance(msg.get("ts"), (int, float)):
                    _ack["ts"] = msg["ts"]
                await ws.send_text(json.dumps(_ack))

            elif msg_type == "agent_diagnostics":
                # Agent WS回退路径: 诊断数据通过信令转发到浏览器
                room_id = msg.get("room_id", "")
                conn = _agent_connections.get(room_id)
                if conn and conn.get("browser_ws"):
                    try:
                        data = {k: v for k, v in msg.items() if k not in ("type", "from_role", "role", "token")}
                        # 扁平化嵌套的data字段
                        if isinstance(data.get("data"), str):
                            try:
                                nested = json.loads(data.pop("data"))
                                data.update(nested)
                            except (json.JSONDecodeError, TypeError):
                                pass
                        elif isinstance(data.get("data"), dict):
                            data.update(data.pop("data"))
                        data["type"] = "agent_diagnostics"
                        data["room_id"] = room_id
                        await conn["browser_ws"].send_text(json.dumps(data))
                        _logger.info("[WS] agent_diagnostics forwarded room=%s", room_id)
                    except Exception as _e:
                        _logger.warning("[WS] agent_diagnostics forward failed: %s", _e)
            elif msg_type == "config_update_ack":
                # Agent confirm config update
                if role == "agent" and agent_id:
                    ok = msg.get("data", {}).get("ok", False)
                    _logger.info(f"[WS] config_update_ack agent={agent_id} ok={ok}")

            elif msg_type == "network_info":
                # Agent→Server: 网络接口清单(注册后/周期扫描/ice_scan_req 回包)
                if role == "agent" and agent_id:
                    await _handle_network_info(agent_id, msg.get("data"))
                else:
                    _logger.warning("[AS-WS] network_info from non-agent role=%r", role)

            elif msg_type == "connection_type":
                # 浏览器上报 WebRTC 连接类型 (P2P/relay/BUG)
                target = msg.get("agent_id", "")
                ct = msg.get("conn_type", "")
                if target in _online_agents:
                    _online_agents[target]["conn_type"] = ct
                    _logger.info("[AS-WS] agent %s conn_type=%s", target, ct)
                    # 持久化到数据库
                    try:
                        async with get_db_session() as _db:
                            await _db.execute(
                                update(Agent).where(Agent.id == target).values(conn_type=ct)
                            )
                            await _db.commit()
                    except Exception as _e:
                        _logger.warning("persist conn_type failed: %s", _e)

            elif msg_type == "list_agents":
                # 浏览器请求在线 agent 列表
                agents_list = []
                for aid, info in _online_agents.items():
                    agents_list.append({
                        "id": aid,
                        "name": info["name"],
                        "version": info["version"],
                        "status": info["status"],
                        "conn_type": info.get("conn_type", ""),
                        "last_seen": info["last_seen"],
                    })
                await ws.send_text(json.dumps({
                    "type": "agent_list",
                    "agents": agents_list,
                }))

            elif msg_type == "list_gateways":
                # 浏览器请求在线 gateway 列表
                gateways_list = []
                for gid, info in _online_gateways.items():
                    gateways_list.append({
                        "id": gid,
                        "name": info["name"],
                        "status": info["status"],
                        "last_seen": info["last_seen"],
                    })
                await ws.send_text(json.dumps({
                    "type": "gateway_list",
                    "gateways": gateways_list,
                }))

    except WebSocketDisconnect:
        _logger.info("[AS-WS] client disconnected role=%s id=%s", role, agent_id or user_id)
    except Exception as e:
        _logger.error("[AS-WS] error %s", e)
    finally:
        # 清理 — R1: gateway 断开时用 agent_id(已写入 gw_id)
        # 仅当在线表仍指向本条 WS 才删除，避免旧连接断开误删新连接
        if role == "agent" and agent_id in _online_agents and _online_agents[agent_id].get("ws") is ws:
            del _online_agents[agent_id]
            _logger.info("[AS-WS] agent %s removed from online list", agent_id)
            # 未回包的 ice_scan_req 随连接作废（挂起方收到 CancelledError → 409）
            _pending = _ice_scan_waiters.pop(agent_id, None)
            if _pending is not None and not _pending.done():
                _pending.cancel()
        elif role == "gateway" and agent_id and agent_id in _online_gateways and _online_gateways[agent_id].get("ws") is ws:
            del _online_gateways[agent_id]
            _logger.info("[GS-WS] gateway %s removed from online list", agent_id)
        # 清理房间 — R10: 同时匹配 gateway_ws
        rooms_to_remove = []
        for room_id, conn in _agent_connections.items():
            if (conn.get("agent_ws") == ws or conn.get("browser_ws") == ws
                    or conn.get("gateway_ws") == ws or conn.get("progress_ws") == ws):
                rooms_to_remove.append(room_id)
        for room_id in rooms_to_remove:
            del _agent_connections[room_id]
        # 测速会话随任一端断开而释放(浏览器断开=取消)
        if _speedtest_active and _speedtest_active.get("room") in rooms_to_remove:
            _speedtest_active = None
        try:
            await ws.close()
        except Exception:
            pass


@router.get("/webrtc/agents")
async def list_agents(user: dict = Depends(get_current_user)):
    """获取在线 wragent 列表 — S2(隔离): 仅返回当前用户可见的 Agent"""
    visible = await _visible_agent_ids(user.get("id", ""))
    agents_list = []
    for aid, info in _online_agents.items():
        if aid not in visible:
            continue
        agents_list.append({
            "id": aid,
            "name": info["name"],
            "version": info["version"],
            "status": info["status"],
            "ip": info.get("ip", "-"),
            "conn_type": info.get("conn_type", ""),
            "last_seen": info["last_seen"],
        })
    return {"agents": agents_list}


@router.get("/webrtc/gateways")
async def list_gateways(user: dict = Depends(get_current_user)):
    """获取在线 wrgateway 列表 — S2(隔离): 仅返回当前用户可见的网关"""
    visible = await _visible_gateway_ids(user.get("id", ""))
    gateways_list = []
    for gid, info in _online_gateways.items():
        if gid not in visible:
            continue
        gateways_list.append({
            "id": gid,
            "name": info["name"],
            "version": info.get("version", ""),
            "status": info["status"],
            "ip": info.get("ip", "-"),
            "last_seen": info["last_seen"],
        })
    return {"gateways": gateways_list}


@router.get("/webrtc/speedtest/history")
def speedtest_history(user: dict = Depends(get_current_user)):
    """最近 20 条测速结果(进程内存, 仅本人发起的)"""
    uid = user.get("id", "")
    items = [h for h in _speedtest_history if h.get("user_id") == uid]
    return {"history": items[-20:]}


@router.get("/webrtc/rooms")
def list_rooms(user: dict = Depends(get_current_user)):
    """获取活跃的 WebRTC 房间 — S3(隔离): 仅本人的房间(含 tunnel 房间不外泄)"""
    uid = user.get("id", "")
    rooms = []
    for room_id, conn in _agent_connections.items():
        if conn.get("user_id") != uid:
            continue
        rooms.append({
            "room_id": room_id,
            "agent_id": conn["agent_id"],
            "user_id": conn["user_id"],
            "created_at": conn["created_at"],
        })
    return {"rooms": rooms}


# ─────────────────────────────────────────────────────────────
# Tailscale 风格 Agent 注册
# ─────────────────────────────────────────────────────────────


def _agent_setup_html() -> str:
    """渲染 Agent 注册页面（支持多语言）"""
    return f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{t('setup.title')} - 泡鱼终端</title>
<style>
  * {{ margin: 0; padding: 0; box-sizing: border-box; }}
  body {{ font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #f5f5f5; display: flex; justify-content: center; align-items: center; min-height: 100vh; }}
  .card {{ background: #fff; border-radius: 12px; box-shadow: 0 2px 12px rgba(0,0,0,0.08); padding: 40px; width: 400px; text-align: center; }}
  .icon {{ font-size: 48px; margin-bottom: 16px; }}
  h1 {{ font-size: 20px; color: #333; margin-bottom: 8px; }}
  .subtitle {{ color: #666; font-size: 14px; margin-bottom: 24px; }}
  .form-group {{ margin-bottom: 16px; text-align: left; }}
  .form-group label {{ display: block; font-size: 13px; color: #555; margin-bottom: 4px; }}
  .form-group input {{ width: 100%; padding: 10px 12px; border: 1px solid #ddd; border-radius: 8px; font-size: 14px; }}
  .form-group input:focus {{ outline: none; border-color: #4a9eff; }}
  .btn {{ width: 100%; padding: 12px; border: none; border-radius: 8px; font-size: 15px; cursor: pointer; margin-top: 8px; }}
  .btn-primary {{ background: #4a9eff; color: #fff; }}
  .btn-primary:hover {{ background: #3a8eef; }}
  .btn-primary:disabled {{ background: #ccc; cursor: not-allowed; }}
  .btn-success {{ background: #28a745; color: #fff; }}
  .msg {{ margin-top: 16px; padding: 12px; border-radius: 8px; font-size: 14px; display: none; }}
  .msg-ok {{ background: #d4edda; color: #155724; display: block; }}
  .msg-err {{ background: #f8d7da; color: #721c24; display: block; }}
  .hint {{ font-size: 12px; color: #999; margin-top: 12px; }}
</style>
</head>
<body>
<div class="card">
  <div class="icon">&#x1F916;</div>
  <h1>{t('setup.title')}</h1>
  <p class="subtitle">{t('setup.subtitle')}</p>

  <div id="login-form">
    <div class="form-group">
      <label>{t('setup.username')}</label>
      <input id="username" type="text" placeholder="admin" autofocus>
    </div>
    <div class="form-group">
      <label>{t('setup.password')}</label>
      <input id="password" type="password" placeholder="{t('setup.password')}">
    </div>
    <button class="btn btn-primary" id="login-btn" onclick="doLogin()">{t('setup.login_btn')}</button>
  </div>

  <div id="register-form" style="display:none">
    <p style="margin-bottom:16px;color:#333">{t('setup.will_register')}</p>
    <button class="btn btn-primary" id="reg-btn" onclick="doRegister()">{t('setup.register_btn')}</button>
  </div>

  <div id="msg" class="msg"></div>
  <p class="hint">Agent ID: <span id="agent-id">-</span></p>
</div>
<script>
const sid = window.location.pathname.split('/').pop();
let jwt = '';
document.getElementById('agent-id').textContent = '{t('setup.loading')}';

fetch('/webrtc/agents/setup/sid/' + sid).then(r=>r.json()).then(d=>{{
  document.getElementById('agent-id').textContent = d.agent_id || '{t('setup.unknown')}';
  if (d.error) {{ showMsg(d.error, true); }}
}}).catch(()=>{{ showMsg('{t('setup.invalid_link')}', true); }});

function showMsg(text, isErr) {{
  const el = document.getElementById('msg');
  el.textContent = text;
  el.className = 'msg ' + (isErr ? 'msg-err' : 'msg-ok');
}}

async function doLogin() {{
  const u = document.getElementById('username').value.trim();
  const p = document.getElementById('password').value;
  if (!u || !p) {{ showMsg('{t('setup.fill_all')}', true); return; }}
  document.getElementById('login-btn').disabled = true;
  try {{
    const r = await fetch('/api/auth/login', {{
      method: 'POST', headers: {{'Content-Type':'application/json'}},
      body: JSON.stringify({{username: u, password: p}})
    }});
    const d = await r.json();
    if (d.token) {{
      jwt = d.token;
      document.getElementById('login-form').style.display = 'none';
      document.getElementById('register-form').style.display = 'block';
      showMsg('{t('setup.login_success')}', false);
    }} else {{
      showMsg(d.detail || '{t('setup.login_failed')}', true);
    }}
  }} catch(e) {{ showMsg('{t('setup.network_error')}', true); }}
  document.getElementById('login-btn').disabled = false;
}}

async function doRegister() {{
  document.getElementById('reg-btn').disabled = true;
  try {{
    const r = await fetch('/webrtc/agents/setup', {{
      method: 'POST',
      headers: {{'Content-Type':'application/json', 'Authorization': 'Bearer ' + jwt}},
      body: JSON.stringify({{sid: sid}})
    }});
    const d = await r.json();
    if (d.ok) {{
      showMsg('{t('setup.register_success')}', false);
      document.getElementById('register-form').style.display = 'none';
    }} else {{
      showMsg(d.detail || '{t('setup.register_failed')}', true);
    }}
  }} catch(e) {{ showMsg('{t('setup.network_error')}', true); }}
  document.getElementById('reg-btn').disabled = false;
}}

document.getElementById('password').addEventListener('keydown', e => {{
  if (e.key === 'Enter') doLogin();
}});
</script>
</body>
</html>"""


@agent_router.get("/install-agent")
async def install_agent(method: str = "", request: Request = None):
    """模式一公开安装脚本（Tailscale 式，无账户）：/install-agent[?method=systemd|docker]"""
    from .auth_api import _get_deploy_config, _generate_install_script
    config = _get_deploy_config(request)
    return PlainTextResponse(content=_generate_install_script(config, method), media_type="text/plain")


@agent_router.get("/agent/s/{sid}")
async def agent_setup_page(sid: str):
    """Tailscale 风格 Agent 注册页面"""
    if sid not in _setup_sessions:
        from fastapi.responses import HTMLResponse
        return HTMLResponse(f"<h2>{t('ws.link_expired')}</h2><p>{t('ws.link_expired_hint')}</p>", status_code=404)
    from fastapi.responses import HTMLResponse
    return HTMLResponse(_agent_setup_html())


@agent_router.get("/webrtc/agents/setup/sid/{sid}")
def agent_setup_info(sid: str):
    """获取 setup 会话信息（供页面读取 agent_id）"""
    session = _setup_sessions.get(sid)
    if not session:
        return {"error": t("ws.link_expired")}
    return {"agent_id": session["agent_id"], "agent_name": session["agent_name"]}


class AgentSetupReq(BaseModel):
    sid: str


@agent_router.post("/webrtc/agents/setup")
async def agent_setup_confirm(req: AgentSetupReq, user: dict = Depends(get_current_user)):
    """用户确认注册 Agent → 创建 agent 并通过 WS 推送 token 给 wragent"""
    session = _setup_sessions.get(req.sid)
    if not session:
        return JSONResponse({"ok": False, "detail": t("ws.link_expired")}, status_code=400)

    agent_id = session["agent_id"]
    agent_name = session["agent_name"]
    ws = session["ws"]

    # 生成 auth_token 并写入数据库
    import secrets as _secrets
    auth_token = _secrets.token_hex(32)
    try:
        async with get_db_session() as db:
            result = await db.execute(select(Agent).where(Agent.id == agent_id))
            agent = result.scalar_one_or_none()
            if agent:
                agent.name = agent_name
                agent.token = auth_token
                agent.remark = t("ws.register_agent", username=user.get("username", "?"))
                if not agent.owner_id:
                    agent.owner_id = user.get("id", "")
            else:
                db.add(Agent(
                    id=agent_id, name=agent_name, token=auth_token,
                    remark=t("ws.register_agent", username=user.get("username", "?")),
                    is_active=True, owner_id=user.get("id", ""),
                    created_at=datetime.now(timezone.utc).isoformat(),
                ))
            await db.commit()
    except Exception as e:
        _logger.warning("webrtc: setup create agent failed: %s", e)
        return JSONResponse({"ok": False, "detail": t("ws.db_error")}, status_code=500)

    # 通过 WS 推送 token 给 wragent
    try:
        await ws.send_text(json.dumps({
            "type": "setup_complete",
            "agent_id": agent_id,
            "data": {"token": auth_token, "agent_id": agent_id},
        }))
    except Exception:
        _logger.warning("webrtc: failed to push token to wragent sid=%s", req.sid)

    # 删除 session
    _setup_sessions.pop(req.sid, None)
    _logger.info("webrtc: agent setup complete agent=%s user=%s", agent_id, user.get("username"))
    return {"ok": True, "agent_id": agent_id}


# L5(延迟): 假活判定阈值与巡检周期 — 心跳 15~30s, 阈值取 2 个心跳周期(60s)，
# 误判可由同连接 heartbeat rehydrate 自愈；巡检 60s→15s 使最坏可见延迟 150s→75s
_AGENT_STALE_SECONDS = 60
_STALE_SWEEP_SECONDS = 15


async def _cleanup_setup_sessions():
    """定时清理过期的 setup 会话（30 分钟）；R9: last_seen 看门狗踢掉假活 agent/gateway"""
    while True:
        await asyncio.sleep(_STALE_SWEEP_SECONDS)
        now = time.time()
        expired = [sid for sid, s in _setup_sessions.items() if now - s["created_at"] > 1800]
        for sid in expired:
            _setup_sessions.pop(sid, None)
            _logger.info("webrtc: setup session expired sid=%s", sid)
        # R9: 超过阈值无心跳视为离线
        for _id, info in list(_online_agents.items()):
            if now - info.get("last_seen", now) > _AGENT_STALE_SECONDS:
                _logger.warning("[AS-WS] agent %s stale (last_seen %.0fs), removing", _id, now - info.get("last_seen", now))
                _online_agents.pop(_id, None)
        for _id, info in list(_online_gateways.items()):
            if now - info.get("last_seen", now) > _AGENT_STALE_SECONDS:
                _logger.warning("[GS-WS] gateway %s stale (last_seen %.0fs), removing", _id, now - info.get("last_seen", now))
                _online_gateways.pop(_id, None)

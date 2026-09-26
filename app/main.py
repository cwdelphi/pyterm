import asyncio
import os
from pathlib import Path

from fastapi import FastAPI, HTTPException, Query
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from app.api_timeline import timeline_router

from app.api_isolated import router as api_router
from app.api_isolated import agent_router
from app.database import init_db, close_db
from app.auth_api import auth_router, admin_router, webrtc_router, deploy_router
from app.i18n import I18nMiddleware

BASE_DIR = Path(__file__).resolve().parent
ROOT_DIR = BASE_DIR.parent
MD_ROOT = Path(os.environ.get("MD_ROOT", str(ROOT_DIR / "md"))).resolve()
STATIC_DIR = ROOT_DIR / "static"
SITE_NAME = os.environ.get("SITE_NAME", "文档中心")
HOME_DOC = os.environ.get("HOME_DOC", "00-欢迎/首页.md")


from contextlib import asynccontextmanager

@asynccontextmanager
async def lifespan(app):
    await init_db()
    # 启动 setup 会话清理任务
    from .api_isolated import _cleanup_setup_sessions
    cleanup_task = asyncio.create_task(_cleanup_setup_sessions())
    yield
    cleanup_task.cancel()
    await close_db()

app = FastAPI(title="泡鱼终端", docs_url=None, redoc_url=None, openapi_url=None, lifespan=lifespan)

# CORS 配置（S7: 默认不开放跨域；由环境变量 CORS_ORIGINS 逗号分隔显式允许）
# 同源部署（静态挂载 / vite proxy）无需 CORS；跨域时设置如 https://a.example,https://b.example
_cors_origins = [o.strip() for o in os.environ.get("CORS_ORIGINS", "").split(",") if o.strip()]
if _cors_origins:
    app.add_middleware(
        CORSMiddleware,
        allow_origins=_cors_origins,
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

# 国际化中间件
app.add_middleware(I18nMiddleware)

# 注册路由
app.include_router(api_router)
app.include_router(agent_router)
app.include_router(auth_router)
app.include_router(admin_router)
app.include_router(timeline_router)
app.include_router(webrtc_router)
app.include_router(deploy_router)


def _build_tree(root: Path):
    nodes = []
    try:
        entries = sorted(
            root.iterdir(),
            key=lambda p: (p.is_file(), p.name.casefold()),
        )
    except OSError:
        return nodes
    for p in entries:
        if p.name.startswith("."):
            continue
        rel = p.relative_to(MD_ROOT).as_posix()
        if p.is_dir():
            children = _build_tree(p)
            if children:
                nodes.append({"name": p.name, "path": rel, "type": "dir", "children": children})
        elif p.suffix.lower() == ".md":
            nodes.append({"name": p.name, "path": rel, "type": "file"})
    return nodes


@app.get("/api/config")
def api_config():
    return {"site_name": SITE_NAME, "home": HOME_DOC}


if STATIC_DIR.is_dir():
    app.mount("/", StaticFiles(directory=STATIC_DIR, html=True), name="static")

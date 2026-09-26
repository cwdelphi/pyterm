"""
后端国际化模块 — 基于 Accept-Language 头 + contextvars
用法:
    from .i18n import t, set_lang
    # 在中间件中: set_lang(request)
    # 在路由/函数中: detail=t("auth.user_not_found")
"""
import json
import contextvars
from pathlib import Path
from typing import Optional

from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request

_lang_var: contextvars.ContextVar[str] = contextvars.ContextVar('lang', default='zh-CN')

_base = Path(__file__).parent / 'locale'
_cache: dict[str, dict] = {}


def _load(lang: str) -> dict:
    if lang not in _cache:
        p = _base / f'{lang}.json'
        if p.exists():
            _cache[lang] = json.loads(p.read_text(encoding='utf-8'))
        else:
            _cache[lang] = {}
    return _cache[lang]


def set_lang(lang: str):
    _lang_var.set(lang)


def get_lang() -> str:
    return _lang_var.get()


def t(key: str, **kwargs) -> str:
    """翻译 key，支持 {name} 占位符"""
    lang = _lang_var.get()
    msgs = _load(lang)
    text = msgs.get(key)
    if text is None and lang != 'zh-CN':
        text = _load('zh-CN').get(key)
    if text is None:
        text = key
    if kwargs:
        try:
            text = text.format(**kwargs)
        except (KeyError, IndexError):
            pass
    return text


def detect_lang(accept_language: Optional[str]) -> str:
    """从 Accept-Language 头解析首选语言"""
    if not accept_language:
        return 'zh-CN'
    for part in accept_language.split(','):
        lang_tag = part.split(';')[0].strip().lower()
        if lang_tag.startswith('en'):
            return 'en'
    return 'zh-CN'


class I18nMiddleware(BaseHTTPMiddleware):
    """从请求头 Accept-Language 检测语言，设置到 contextvars"""
    async def dispatch(self, request: Request, call_next):
        lang = detect_lang(request.headers.get('accept-language'))
        set_lang(lang)
        response = await call_next(request)
        return response

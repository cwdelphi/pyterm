# 国际化方案 v1.2

> 创建日期: 2026-09-22
> 更新日期: 2026-09-22
> 状态: 第二期完成
> 范围: 第一期 — 前端国际化 (vue-i18n) + 第二期 — 后端国际化

## 1. 完成情况

### 第一期：前端国际化 (vue-i18n)

| 项目 | 状态 |
|------|------|
| vue-i18n 安装 | ✅ |
| i18n 实例 (`src/i18n/index.ts`) | ✅ |
| 中文语言包 (`zh-CN.json`) | ✅ |
| 英文语言包 (`en.json`) | ✅ |
| main.ts 注册 | ✅ |
| localStorage 持久化 | ✅ |
| TopBar 语言切换按钮 `[中/EN]` | ✅ |
| 21 个 Vue 组件替换 | ✅ |
| 构建验证 | ✅ 通过 |

### 第二期：后端国际化 (本版新增)

| 项目 | 状态 |
|------|------|
| `app/i18n.py` 中间件 + contextvars | ✅ |
| `app/locale/zh-CN.json`、`en.json` 语言包 | ✅ 各 123 keys |
| main.py 注册 I18nMiddleware | ✅ |
| auth.py 替换 | ✅ 13 处 |
| auth_api.py 替换 | ✅ 38 处 |
| api.py 替换 | ✅ 45 处 |
| api_isolated.py 替换 | ✅ 68 处（含 Agent 注册 HTML 页面） |
| api_timeline.py 替换 | ✅ 38 处 |
| 容器重启 | ✅ |
| 中英双语接口实测 | ✅ |

### 附加：移除全局搜索组件

| 项目 | 状态 |
|------|------|
| GlobalSearch.vue 删除 | ✅ |
| App.vue 引用清理 | ✅ |
| TopBar 搜索按钮/⌘K emit 移除 | ✅ |
| globalSearch i18n 键清理 | ✅ |
| 前端构建验证 | ✅ |

## 2. 后端实现机制

- **语言检测**: `I18nMiddleware` 读取请求头 `Accept-Language`,通过 `detect_lang()` 判定 `en` / `zh-CN`(缺省中文)
- **上下文传递**: `contextvars.ContextVar('lang')`,中间件 `set_lang()` 在每个请求线程内注入,路由内任意函数可 `t('key')` 取词
- **翻译函数**: `t(key, **kwargs)` 支持 `{placeholder}` 占位符,英文缺词自动回退中文
- **时序步骤**: `api_timeline.py` 的 action/dst_ip 等字段也接入翻译,与前端诊断面板联动

## 3. 语言包结构

```
app/
├── i18n.py            # I18nMiddleware + set_lang/get_lang + t() + detect_lang()
└── locale/
    ├── zh-CN.json     # 中文 (123 keys)
    └── en.json        # 英文 (123 keys)
```

按模块组织：auth, admin, file, link, ssh, sftp, vnc, ws, setup, timeline

## 4. 第二期替换清单

| 文件 | 替换数 | 说明 |
|------|:---:|------|
| auth.py | 13 | 登录/lockout/权限 detail |
| auth_api.py | 38 | 用户管理/Agent/Gateway/coturn 权限 |
| api.py | 45 | 文件/链接/SSH/SFTP |
| api_isolated.py | 68 | WebSocket 信令 + Agent 注册 HTML 页 |
| api_timeline.py | 38 | 诊断时序步骤 action/name |
| **合计** | **202** | |

## 5. 未替换项（不需要国际化）

- `models.py`: Pydantic Field description(仅 API 文档元数据)
- log 消息：`_log_sftp("已生成主机密钥")` 等调试日志
- 代码注释/docstring
- database.py 种子数据（如「未分组」「默认coturn」存储用名）

## 6. 后续优化

- [ ] 语言包按模块拆分为独立文件（避免单文件过大）
- [ ] 浏览器自动检测语言（navigator.language）
- [ ] Agent 二进制(wragent/wrgateway)错误消息国际化
- [ ] Swagger/OpenAPI 文档多语言
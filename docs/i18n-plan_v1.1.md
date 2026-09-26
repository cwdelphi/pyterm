# 国际化方案 v1.1

> 创建日期: 2026-09-22
> 更新日期: 2026-09-22
> 状态: 第一期完成（第二期已完成，见 [i18n-plan_v1.2.md](./i18n-plan_v1.2.md)）
> 范围: 第一期 — 前端国际化 (vue-i18n)

## 1. 完成情况

| 项目 | 状态 |
|------|------|
| vue-i18n 安装 | ✅ |
| i18n 实例 (`src/i18n/index.ts`) | ✅ |
| 中文语言包 (`zh-CN.json`) | ✅ ~350 keys |
| 英文语言包 (`en.json`) | ✅ ~350 keys |
| main.ts 注册 | ✅ |
| localStorage 持久化 | ✅ |
| TopBar 语言切换按钮 `[中/EN]` | ✅ |
| 21 个 Vue 组件替换 | ✅ |
| 遗漏字符串修复 | ✅ |
| 构建验证 | ✅ 通过 |
| 容器重启 | ✅ |

## 2. 已替换组件清单

| 组件 | 中文行数 | 状态 |
|------|:---:|:---:|
| AdminPanel.vue | 301 | ✅ |
| SshManager.vue | 120 | ✅ |
| DocManager.vue | 70 | ✅ |
| Login.vue | 68 | ✅ |
| LinkManager.vue | 52 | ✅ |
| GatewayManager.vue | 48 | ✅ |
| SftpManager.vue | 44 | ✅ |
| SshFileBrowser.vue | 43 | ✅ |
| AgentConfigPage.vue | 41 | ✅ |
| DiagnosisPanel.vue | 30 | ✅ |
| Register.vue | 20 | ✅ |
| VncViewer.vue | 20 | ✅ |
| TopBar.vue | 17 | ✅ |
| GlobalSearch.vue | 5 | ✅ |
| FileTreeNode.vue | 5 | ✅ |
| Breadcrumb.vue | 2 | ✅ |
| SideNav.vue | 1 | ✅ |
| EmptyState.vue | 1 | ✅ |
| api.ts | 22 | ⏳ (后端返回) |
| App.vue | 7 | ✅ |

## 3. 未替换项（不需要国际化）

- `api.ts`: 错误消息由后端返回，属于后端国际化范畴
- `webrtc.ts`: 内部错误消息，仅用于 console/debug
- `editor.ts`: 独立 Markdown 编辑器页面，非主应用
- 代码注释：开发者注释，保持中文
- console.log/warn/error：调试日志，保持中文

## 4. 语言包结构

```
web/src/i18n/
├── index.ts          # i18n 实例 + setLocale/getLocale
├── zh-CN.json        # 中文 (~350 keys)
└── en.json           # 英文 (~350 keys)
```

按模块组织：common, nav, site, topbar, auth, home, docs, links, ssh, admin, agentConfig, sftp, sftpBrowser, diagnosis, vnc, globalSearch, fileTree, breadcrumb, empty, api

## 5. 第二期：后端国际化（暂缓）

| 步骤 | 内容 | 涉及文件 |
|------|------|---------|
| 1 | FastAPI 中间件读取 Accept-Language | 新建 app/i18n.py |
| 2 | 错误消息多语言字典 | 新建 app/locale/zh.json、en.json |
| 3 | 替换 detail="中文" → detail=t("key") | auth.py、auth_api.py、api.py 等 |

## 6. 后续优化

- [ ] 语言包按模块拆分为独立文件（避免单文件过大）
- [ ] 浏览器自动检测语言（navigator.language）
- [ ] 后端 API 错误消息国际化
- [ ] Swagger/OpenAPI 文档多语言

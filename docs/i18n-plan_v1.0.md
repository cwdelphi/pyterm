# 国际化方案 v1.0

> 创建日期: 2026-09-22
> 状态: 执行中
> 范围: 第一期 — 前端国际化 (vue-i18n)

## 1. 现状评估

| 维度 | 状态 |
|------|------|
| i18n 库 | 未安装，无 vue-i18n |
| 语言文件 | 不存在，无 locale/ 目录 |
| 语言切换器 | 无 |
| 前端中文字符串 | ~895 行，分布在 21 个文件 |
| 后端中文字符串 | ~504 行，分布在 10 个 Python 文件 |
| **总计需处理** | **~1,400 行** |

## 2. 方案：分前后端两期

### 第一期：前端国际化（vue-i18n）

| 步骤 | 内容 | 涉及文件 |
|------|------|---------|
| 1 | 安装 vue-i18n | web/package.json |
| 2 | 创建 i18n 实例 + 语言包 | 新建 web/src/i18n/index.ts、zh-CN.json、en.json |
| 3 | 注册 i18n 插件 | web/src/main.ts |
| 4 | 语言切换器 | web/src/components/TopBar.vue |
| 5 | 逐组件替换硬编码中文 → $t('key') | 21 个 Vue 文件 + api.ts + App.vue |
| 6 | 语言偏好持久化 | localStorage，缺省 zh-CN |

### 第二期：后端国际化（暂缓）

| 步骤 | 内容 | 涉及文件 |
|------|------|---------|
| 1 | FastAPI 中间件读取 Accept-Language | 新建 app/i18n.py |
| 2 | 错误消息多语言字典 | 新建 app/locale/zh.json、en.json |
| 3 | 替换 detail="中文" → detail=t("key") | auth.py、auth_api.py、api.py 等 |

## 3. 语言包结构

```json
{
  "nav": { "docs": "文档管理", "links": "网址管理", "ssh": "SSH管理", "admin": "管理面板" },
  "login": { "title": "泡鱼终端", "username": "用户名", "password": "密码", "submit": "登录" },
  "admin": { "users": "用户管理", "agents": "Agent管理", "gateways": "网关管理", "downloads": "程序下载" },
  "toast": { "saved": "保存成功", "deleted": "删除成功", "copied": "已复制" },
  "error": { "auth_expired": "认证已过期，请重新登录", "not_found": "资源不存在", "forbidden": "无权操作" }
}
```

## 4. 前端工作量明细

| 文件 | 中文行数 | 优先级 |
|------|:---:|:---:|
| AdminPanel.vue | 301 | 高 |
| SshManager.vue | 120 | 高 |
| DocManager.vue | 70 | 高 |
| Login.vue | 68 | 高 |
| LinkManager.vue | 52 | 高 |
| GatewayManager.vue | 48 | 高 |
| SftpManager.vue | 44 | 中 |
| SshFileBrowser.vue | 43 | 中 |
| AgentConfigPage.vue | 41 | 中 |
| DiagnosisPanel.vue | 30 | 中 |
| Register.vue | 20 | 中 |
| VncViewer.vue | 20 | 中 |
| TopBar.vue | 17 | 高 |
| GlobalSearch.vue | 5 | 低 |
| FileTreeNode.vue | 5 | 低 |
| Breadcrumb.vue | 2 | 低 |
| SideNav.vue | 1 | 低 |
| EmptyState.vue | 1 | 低 |
| api.ts | 22 | 高 |
| App.vue | 7 | 高 |

## 5. 语言切换器设计

放在 TopBar.vue 右侧，与主题切换按钮并列：

```
[🌙] [中/EN]
```

点击切换，立即生效，存 localStorage('lang')。缺省中文。

## 6. 建议

- 先做第一期（前端），用户可感知，投入产出比高
- 后端错误消息前端可先做映射翻译（不改后端代码），后期再做后端原生 i18n
- 语言包按模块拆分（login.json、admin.json、ssh.json...），避免单文件过大

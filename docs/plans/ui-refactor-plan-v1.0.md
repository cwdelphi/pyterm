# UI 重构方案 v1.0

> **版本**: 1.0
> **日期**: 2026-09-20
> **状态**: 执行中
> **作者**: opencode + cw

---

## 一、目标

基于 `docs/ui-design-system.md` 设计规范，对现有系统界面进行重构，目标是更好的用户交互体验。

### 设计原则（Anti-Empty Space）

- 视口撑满（min-height: 100vh / flex: 1）
- Flex 弹性填充（page-container → page-body flex:1）
- 背景层级区分（bg: #f0f2f5 / panel: #ffffff）
- 表格 flex:1 自适应，禁止大面积空白
- 空状态必须占位组件
- 卡片等高（align-items: stretch / Grid）
- 分页器 flex-shrink: 0 固定底部
- scoped 样式强制

---

## 二、现状审查

### 2.1 组件合规性审查表

| 组件 | script setup | scoped | 高度处理 | 空状态 | flex:1 | table-card | 问题 |
|------|:---:|:---:|:---:|:---:|:---:|:---:|------|
| AdminPanel | ✅ | ✅ | ❌ calc(100vh-54px) | ✅ | ✅ | ✅ 部分 | 统计区无卡片网格，日志无分页 |
| AgentConfigPage | ✅ | ✅ | ❌ calc(100vh-54px) | ✅ | ✅ | ❌ | 内容稀疏，无骨架屏 |
| AgentManager | ✅ | ✅ | ❌ calc(100vh-54px) | ✅ | ❌ | ❌ | 无 table-card 包裹 |
| Breadcrumb | ✅ | ❌ 无样式 | N/A | N/A | N/A | N/A | 无 scoped 样式 |
| DiagnosisPanel | ✅ | ✅ | ✅ max-height | ✅ | ❌ | ❌ | Catppuccin 配色，未接入全局主题 |
| DirSelectNode | ✅ | ❌ 无样式 | N/A | N/A | N/A | N/A | 无 scoped 样式 |
| DocManager | ✅ | ✅ | ❌ calc(100vh-54px) | ✅ | ✅ | ❌ | sidebar+main 正确但无 table-card |
| FileTreeNode | ✅ | ✅ | N/A | ✅ | ✅ | N/A | OK |
| GatewayManager | ✅ | ✅ | ❌ 无高度约束 | ✅ | ❌ | ✅ | 无 page-container，无分页 |
| GlobalSearch | ✅ | ✅ | ✅ 遮罩 | ✅ | ❌ | N/A | OK |
| LinkManager | ✅ | ✅ | ❌ calc(100vh-54px) | ✅ | ✅ | ❌ | sidebar+main 正确，卡片用 Grid |
| Login | ✅ | ✅ | ✅ min-height:100vh | N/A | N/A | N/A | OK |
| MarkdownView | ✅ | ❌ 无样式 | N/A | N/A | N/A | N/A | 无 scoped 样式 |
| Register | ✅ | ✅ | ✅ min-height:100vh | N/A | N/A | N/A | OK |
| SftpManager | ✅ | ✅ | ❌ 无高度约束 | ✅ | ✅ | ❌ | 无 page-container |
| SideNav | ✅ | ✅ | N/A | ✅ | ❌ | N/A | OK |
| SshFileBrowser | ✅ | ✅ | N/A | ✅ | ✅ | N/A | OK |
| SshManager | ✅ | ✅ | ❌ calc(100vh-54px) | ✅ | ✅ | ❌ | sidebar+main 正确 |
| Toast | ✅ | ✅ | N/A | N/A | N/A | N/A | OK |
| TopBar | ✅ | ✅ | N/A | N/A | N/A | N/A | OK |
| VncViewer | ✅ | ✅ | N/A | N/A | ✅ | N/A | OK |

### 2.2 核心问题汇总

| # | 问题类别 | 涉及组件 | 严重度 | 影响 |
|---|---------|---------|:---:|------|
| 1 | 硬编码高度 calc(100vh-54px) | Admin/Agent/Doc/Link/Ssh (5个) | 🔴 高 | TopBar高度变化时布局错乱 |
| 2 | 重复布局模式 | Doc/Link/Ssh/Admin (4个) | 🟡 中 | sidebar+main 各自实现，无复用 |
| 3 | DiagnosisPanel 孤立设计 | DiagnosisPanel | 🟡 中 | Catppuccin 配色不随主题切换 |
| 4 | 表格无分页 | Gateway/Admin 部分表格 | 🟡 中 | 数据量大时性能差 |
| 5 | 空状态不统一 | Gateway/Agent/Sftp | 🟡 中 | 部分用 empty-row，部分自定义 |
| 6 | 无骨架屏 | 所有列表页 | 🟠 低 | 加载态闪烁 |
| 7 | 4组件无 scoped | Breadcrumb/DirSelectNode/MarkdownView | 🟠 低 | 样式污染风险 |
| 8 | 按钮样式不一致 | AgentManager/GatewayManager | 🟠 低 | 部分用 .btn，部分内联 |

---

## 三、重构方案

### 阶段 1：全局基础系统

**目标**：建立统一的设计令牌、页面容器、空状态组件、骨架屏组件

**改动文件**：

| 文件 | 操作 | 改动内容 |
|------|:---:|---------|
| styles/theme.css | 编辑 | 新增间距/圆角/状态色令牌 |
| styles/layout.css | 编辑 | 新增 page-container/page-header/page-body/card-grid/page-empty/status-badge/breadcrumb/skeleton |
| components/EmptyState.vue | 新建 | 统一空状态组件 |
| components/SkeletonRow.vue | 新建 | 骨架屏行组件 |

### 阶段 2：TopBar 导航优化

**改动文件**：

| 文件 | 操作 | 改动内容 |
|------|:---:|---------|
| TopBar.vue | 编辑 | .topmenu.active 增加 background:var(--accent-soft) |

### 阶段 3：AdminPanel 重构

**改动文件**：

| 文件 | 操作 | 改动内容 |
|------|:---:|---------|
| AdminPanel.vue | 编辑 | 布局改为 page-container，表格加 table-header，空状态统一 EmptyState，加载时显示 SkeletonRow，日志加分页 |

### 阶段 4：六页面统一修复

**改动文件**：

| 文件 | 操作 | 改动内容 |
|------|:---:|---------|
| SshManager.vue | 编辑 | calc(100vh-54px) → height:100% + 面包屑 |
| DocManager.vue | 编辑 | calc(100vh-54px) → height:100% + 面包屑 |
| LinkManager.vue | 编辑 | calc(100vh-54px) → height:100% + 面包屑 |
| AgentManager.vue | 编辑 | calc(100vh-54px) → height:100% + 面包屑 + table-card + 分页 |
| SftpManager.vue | 编辑 | 无高度约束 → height:100% + 面包屑 + table-card + 分页 |
| GatewayManager.vue | 编辑 | 无高度约束 → height:100% + 面包屑 + table-header + 分页 |

### 阶段 5：DiagnosisPanel 主题接入

**改动文件**：

| 文件 | 操作 | 改动内容 |
|------|:---:|---------|
| DiagnosisPanel.vue | 编辑 | Catppuccin 配色替换为全局 CSS 变量 |

**变量映射**：

| 当前（Catppuccin） | 目标（全局变量） |
|---|---|
| --panel: #1e1e2e | var(--panel) |
| --text: #cdd6f4 | var(--fg) |
| --border: #313244 | var(--border) |
| --text-dim: #a6adc8 | var(--muted) |
| --accent: #89b4fa | var(--accent) |
| --hover: #313244 | var(--panel-2) |
| --surface: #181825 | var(--bg) |
| #a6e3a1 | var(--btn-success) |
| #f38ba8 | var(--btn-danger) |
| #f9e2af | var(--btn-warning) |

### 阶段 6：4个无样式组件补充 scoped

**改动文件**：

| 文件 | 操作 | 改动内容 |
|------|:---:|---------|
| Breadcrumb.vue | 编辑 | 新增 style scoped |
| DirSelectNode.vue | 编辑 | 新增 style scoped |
| MarkdownView.vue | 编辑 | 新增 style scoped |
| AgentConfigPage.vue | 编辑 | 高度 calc(100vh-54px) → height:100% + 骨架屏 |

---

## 四、实施顺序

```
阶段1 全局基础 → 阶段2 TopBar → 阶段3 AdminPanel → 阶段4 六页面统一 → 阶段5 DiagnosisPanel → 阶段6 无样式组件
```

## 五、文件改动总览

| 文件 | 阶段 | 操作 |
|------|:---:|:---:|
| styles/theme.css | 1 | 编辑 |
| styles/layout.css | 1 | 编辑 |
| components/EmptyState.vue | 1 | 新建 |
| components/SkeletonRow.vue | 1 | 新建 |
| components/TopBar.vue | 2 | 编辑 |
| components/AdminPanel.vue | 3 | 编辑 |
| components/SshManager.vue | 4 | 编辑 |
| components/DocManager.vue | 4 | 编辑 |
| components/LinkManager.vue | 4 | 编辑 |
| components/AgentManager.vue | 4 | 编辑 |
| components/SftpManager.vue | 4 | 编辑 |
| components/GatewayManager.vue | 4 | 编辑 |
| components/DiagnosisPanel.vue | 5 | 编辑 |
| components/Breadcrumb.vue | 6 | 编辑 |
| components/DirSelectNode.vue | 6 | 编辑 |
| components/MarkdownView.vue | 6 | 编辑 |
| components/AgentConfigPage.vue | 6 | 编辑 |

**总计**：修改 15 个文件 + 新建 2 个组件

## 六、验证清单

- [ ] 亮色/暗色主题切换所有页面正常
- [ ] 蓝/绿/紫强调色切换正常
- [ ] 所有页面无 calc(100vh - 54px) 残留
- [ ] 表格空状态显示统一 EmptyState 组件
- [ ] 分页器固定底部不跳动
- [ ] 卡片网格等高对齐
- [ ] DiagnosisPanel 状态颜色随主题变化
- [ ] 移动端响应式正常（900px/640px 断点）
- [ ] 所有 scoped 样式无污染
- [ ] 正常业务通信延迟无影响

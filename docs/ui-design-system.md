# 后台管理系统 UI 设计系统规范（Vue 3.0）

> **适用范围**：本项目所有后台管理页面（Vue 3.0 + `<script setup>`）
> **核心组件**：表格（Table）、卡片（Card）
> **设计理念**：紧凑、数据密集、零留空、专业高效
> **参考组件库**：Element Plus（如未使用请忽略组件名前缀，仅遵守布局原则）

---

## 一、全局布局原则（Anti-Empty Space）

### 1.1 视口撑满
- 所有页面根容器必须撑满整个浏览器视口。
- 页面最外层 `div`（或 `<el-container>`）必须设置 `min-height: 100vh` 或通过 `flex-grow: 1` 撑满。
- **严禁**使用硬编码高度（如 `height: 800px`）作为页面主容器高度。

### 1.2 Flex 弹性填充
- 页面模板结构统一采用纵向 Flex 布局：
  ```html
  <template>
    <div class="page-container">
      <div class="page-header">...</div>
      <div class="page-body">
        <!-- 表格或卡片内容 -->
      </div>
    </div>
  </template>
  ```
  ```css
  .page-container {
    display: flex;
    flex-direction: column;
    min-height: 100vh; /* 或 flex: 1 */
  }
  .page-body {
    flex: 1;
    overflow: auto;
  }
  ```
- `.page-body` 必须使用 `flex: 1` 吸收导航栏/面包屑之外的所有剩余空间。
- 即使数据只有一行，表格/卡片容器也不得塌陷。

### 1.3 背景与层级
- 页面整体背景色（`#f0f2f5`）与卡片/表格背景色（`#ffffff`）需有明确层级区分。
- 容器圆角 4px–8px，阴影轻薄，符合后台系统紧凑风格。

---

## 二、表格（Table）组件规范

### 2.1 尺寸与自适应
- 表格宽度 `100%` 自适应父容器。
- 使用 Element Plus 时：`<el-table :data="list" height="100%">` 配合外层 `flex: 1` 实现自动撑满。
- 表头固定：`height` 属性或 `max-height` 配合 `flex` 使用，确保表头不随滚动消失。
- **禁止**表格下方出现大面积空白——表格容器必须 `flex: 1` 填满父级。

### 2.2 列与内容
- 列宽合理分配，核心业务字段优先展示。
- 文本溢出使用 `show-overflow-tooltip` 属性（Element Plus）或 CSS 省略号。
- 禁止内容折行撑高行高，保持行高统一（建议 40px–48px）。

### 2.3 空状态
- 无数据时**严禁**留白。必须：
  - 使用 `<el-empty>` 或自定义空状态组件
  - 空状态容器保持 `min-height: 300px` 或 `flex: 1` 居中显示
  - 维持页面结构稳定，底部不裸露

### 2.4 分页
- 分页器（`<el-pagination>`）固定在表格底部，包裹在 `flex` 容器内 `margin-top: auto` 或独立 `flex-shrink: 0` 区域。
- 不随数据量变化上下跳动。

---

## 三、卡片（Card）组件规范

### 3.1 排列与等高
- 卡片列表使用 CSS Grid 或 Flex 布局：
  ```css
  .card-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 16px;
    align-items: stretch; /* 等高 */
  }
  ```
- 同一行卡片底部必须齐平，消除因内容多少不同导致的底部参差空白。

### 3.2 内部间距
- 卡片内边距紧凑：建议 `16px–24px`。
- 内容过少时使用骨架屏（`<el-skeleton>`）或默认提示填充，禁止卡片高度塌陷。

### 3.3 卡片模板结构
```html
<el-card class="data-card" shadow="hover">
  <template #header>
    <span class="card-title">统计指标</span>
  </template>
  <div class="card-body">
    <!-- 内容 -->
  </div>
</el-card>
```
```css
.data-card {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.card-body {
  flex: 1;
}
```

---

## 四、Vue 3 SFC 编码约定

### 4.1 组件结构
- 使用 `<script setup>` 语法糖。
- 样式必须 `scoped`，全局布局变量放 `:root` 或单独的 `variables.scss`。
- 页面级组件放在 `views/` 下，业务组件放在 `components/` 下。

### 4.2 响应式高度处理
- 当 `flex` 无法满足动态高度需求时，使用 `useResizeObserver` 或 `onMounted` 中 `calc()` 计算：
  ```js
  const tableHeight = ref(0)
  onMounted(() => {
    tableHeight.value = window.innerHeight - headerOffset - footerOffset
  })
  ```
- 但**优先使用 CSS Flex**，避免 JS 计算。

### 4.3 空状态统一处理
- 封装一个 `<EmptyState>` 组件，所有列表/表格无数据时统一调用。
- 组件内部保证 `min-height` 和居中布局。

---

## 五、禁止事项（Hard Constraints）

| 禁止行为 | 正确做法 |
|---------|---------|
| 硬编码 `height: 800px` | 使用 `flex: 1` / `min-height: 100vh` |
| 表格无数据时留白 | 渲染 `<el-empty>` 占位 |
| 卡片高度参差不齐 | `align-items: stretch` 或 Grid 等高 |
| 页面底部裸露空白 | 根容器 `min-height: 100vh` + `flex` |
| 内容溢出撑开布局 | `overflow: auto` + `text-overflow: ellipsis` |
| 使用 `<style>` 无 scoped | 必须 `scoped` 或 CSS Modules |
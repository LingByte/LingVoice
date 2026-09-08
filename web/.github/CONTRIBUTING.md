# Contributing to LingVoice

感谢您考虑为 **LingVoice** 做贡献！无论是报告 Bug、提出建议、添加功能还是完善文档，每一份贡献都很有价值。

## 目录

1. [快速开始](#快速开始)
2. [如何贡献](#如何贡献)
3. [代码规范](#代码规范)
4. [Pull Request 指南](#pull-request-指南)
5. [报告问题](#报告问题)
6. [社区准则](#社区准则)

---

## 快速开始

1. **Fork** 本仓库
2. **Clone** 你的 Fork：

```bash
git clone https://github.com/<your-username>/LingVoice.git
cd LingVoice/web
```

3. 安装依赖：

```bash
pnpm install
```

4. 启动开发服务器：

```bash
pnpm dev
```

## 如何贡献

### 报告 Bug

使用 GitHub Issues 提交 Bug 报告，请包含：

- 清晰的 Bug 描述
- 复现步骤
- 期望行为与实际行为
- 截图（如适用）

### 提交功能建议

使用 GitHub Issues 提交功能请求，请描述：

- 该功能解决的问题
- 期望的解决方案
- 考虑过的替代方案

### 提交代码

1. 创建功能分支：`git checkout -b feat/your-feature`
2. 编写代码并确保通过检查：`pnpm lint && pnpm format:check && pnpm build`
3. 提交代码，遵循 [Conventional Commits](https://www.conventionalcommits.org/)
4. 提交 Pull Request

## 代码规范

- 使用 TypeScript 严格模式
- 使用 ESLint + Prettier 进行代码格式化
- 组件使用函数式组件 + Hooks
- 遵循 Shadcn UI 组件规范
- 提交信息遵循 Conventional Commits 规范

## Pull Request 指南

- PR 标题遵循 Conventional Commits 格式（如 `feat: xxx`、`fix: xxx`）
- 确保 CI 检查通过（lint、build、test）
- 如有 UI 变更，请附截图
- 关联相关 Issue

## 报告问题

- [Bug 报告](https://github.com/LingByte/LingVoice/issues/new?template=bug-report.md)
- [功能请求](https://github.com/LingByte/LingVoice/issues/new?template=feature-request.md)

## 社区准则

请遵守 [Code of Conduct](CODE_OF_CONDUCT.md)，保持友善和尊重。

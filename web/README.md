# LingVoice Web

LingVoice 统一智能语音模型聚合与分发平台的 Web 管理界面。

基于 Shadcn UI + Vite + TanStack Router 构建，提供语音模型全生命周期管理、
用户/角色/权限管理、对象存储管理、API 文档等功能的可视化操作界面。

## 功能

- 明暗主题切换
- 响应式布局（桌面 / 平板 / 移动端）
- 无障碍访问支持
- 内置侧边栏导航
- 全局搜索命令面板
- 10+ 管理页面
- RTL 多语言支持

## 技术栈

| 类别 | 技术 |
|------|------|
| UI 组件 | [Shadcn UI](https://ui.shadcn.com) (TailwindCSS + RadixUI) |
| 构建工具 | [Vite](https://vitejs.dev/) |
| 路由 | [TanStack Router](https://tanstack.com/router/latest) |
| 类型检查 | [TypeScript](https://www.typescriptlang.org/) |
| 代码规范 | [ESLint](https://eslint.org/) & [Prettier](https://prettier.io/) |
| 图标 | [Lucide Icons](https://lucide.dev/icons/) |
| 状态管理 | [Zustand](https://github.com/pmndrs/zustand) |
| 数据请求 | [TanStack Query](https://tanstack.com/query) + [Axios](https://axios-http.com/) |
| 表单 | [React Hook Form](https://react-hook-form.com/) + [Zod](https://zod.dev/) |
| 图表 | [Recharts](https://recharts.org/) |

## 本地开发

```bash
# 安装依赖
pnpm install

# 启动开发服务器
pnpm dev

# 构建生产版本
pnpm build

# 代码检查
pnpm lint

# 格式化
pnpm format

# 运行测试
pnpm test
```

## 项目结构

```
web/
├── src/
│   ├── components/     # UI 组件 (Shadcn + 自定义)
│   ├── features/        # 业务功能模块
│   ├── routes/          # 路由页面 (TanStack Router 文件式路由)
│   ├── hooks/           # 自定义 Hooks
│   ├── stores/          # Zustand 状态管理
│   ├── lib/             # 工具函数
│   ├── config/          # 配置文件
│   ├── context/         # React Context
│   ├── styles/          # 全局样式
│   └── assets/          # 静态资源
├── public/              # 公共资源
└── .github/            # CI/CD 与社区规范
```

## 与后端集成

Web 前端通过 HTTP API 与 LingVoice 后端服务 (`cmd/server`) 通信：

- API 基础路径：`/api/v1`
- 认证：JWT (Access + Refresh Token)
- API 文档：后端 `/docs` 路径提供 OpenAPI 3.1 文档

## License

[MIT](LICENSE) © LingByte

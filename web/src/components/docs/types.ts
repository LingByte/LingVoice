/** 侧边栏导航（public/docs/nav.json） */
export interface DocsNavFile {
  version: number
  groups: DocsNavGroup[]
}

export interface DocsNavGroup {
  id: string
  title: string
  /** 是否默认展开分组 */
  defaultExpanded?: boolean
  items: DocsNavItem[]
}

export interface DocsNavItem {
  id: string
  label: string
  /** 对应 public/docs/pages/{page}.json（不含扩展名） */
  page: string
  /** 供 Cmd+K 搜索的额外关键词，空格分隔 */
  keywords?: string
  icon?: string
  /** 展示在标题右侧的小标签，如「必读」或 HTTP 方法 */
  badge?: string
  /** Arco Tag 颜色 */
  badgeColor?: 'arcoblue' | 'cyan' | 'green' | 'orange' | 'purple' | 'magenta' | 'red' | 'orangered' | 'gold' | 'lime' | 'pink' | 'gray'
  /**
   * 与 openapi paths 对齐，用于 Cmd+K 与 openapi 条目合并。
   * 格式：`METHOD:/path`，path 与 openapi.json 的 paths 键一致，如 `POST:/chat/completions`。
   */
  openApiKey?: string
}

/** 单页文档（public/docs/pages/*.json） */
export interface DocsPageFile {
  title: string
  subtitle?: string
  blocks: DocBlock[]
}

export type DocBlock =
  | { type: 'markdown'; content: string }
  | { type: 'code'; language: string; code: string; title?: string; readOnly?: boolean }
  | {
      type: 'callout'
      variant?: 'info' | 'warning' | 'success' | 'error'
      title?: string
      content: string
    }
  | { type: 'collapse'; title: string; defaultOpen?: boolean; blocks: DocBlock[] }
  | {
      type: 'openapiOperation'
      method: string
      /** OpenAPI paths 中的键，如 /chat/completions */
      path: string
      /** 是否展示调试面板（默认 true） */
      showTry?: boolean
      /** 请求体编辑器初始内容（合法 JSON 字符串） */
      defaultBody?: string
    }
  | { type: 'playground' }

export interface DocsSearchEntry {
  id: string
  label: string
  subtitle: string
  keywords: string
}

/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string
  /** 开发时可选：无 localStorage token 时作为 Authorization 回退（勿提交真实密钥） */
  readonly VITE_AUTH_BEARER_FALLBACK?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

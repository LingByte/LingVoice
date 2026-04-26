import { LlmUsagePage } from '@/pages/LlmUsagePage'

/** 当前用户 LLM 使用日志（非管理员入口） */
export function UsageLogsPage() {
  return <LlmUsagePage variant="user" />
}

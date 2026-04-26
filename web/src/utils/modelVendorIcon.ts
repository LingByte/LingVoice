/**
 * 与 new-api 思路一致：优先自定义 icon_url；否则用 vendor / 模型名推断，
 * 图标来自 Simple Icons（CDN），避免引入 @lobehub/icons 大包。
 */
const SI_VER = '13.21.0'

export function simpleIconCDN(slug: string): string {
  return `https://cdn.jsdelivr.net/npm/simple-icons@${SI_VER}/icons/${slug}.svg`
}

/** vendor 小写关键字 → simple-icons slug */
const VENDOR_SLUG: Record<string, string> = {
  openai: 'openai',
  anthropic: 'anthropic',
  google: 'google',
  deepseek: 'deepseek',
  meta: 'meta',
  mistral: 'mistralai',
  xai: 'x',
  cohere: 'cohere',
  moonshot: 'moonshot',
  alibaba: 'alibabacloud',
  qwen: 'alibabacloud',
  ibm: 'ibm',
  perplexity: 'perplexity',
  microsoft: 'microsoft',
  aws: 'amazonaws',
  zhipu: 'zhipu',
  baichuan: 'baichuan',
  baidu: 'baidu',
  tencent: 'tencent',
}

export function guessVendorFromModelName(model: string): string {
  const m = model.toLowerCase()
  if (m.includes('claude')) return 'anthropic'
  if (
    m.includes('gpt') ||
    m.startsWith('o1') ||
    m.startsWith('o3') ||
    m.includes('davinci') ||
    m.includes('text-embedding-3') ||
    m.includes('chatgpt')
  )
    return 'openai'
  if (m.includes('gemini') || m.includes('palm')) return 'google'
  if (m.includes('deepseek')) return 'deepseek'
  if (m.includes('llama') || m.includes('meta-')) return 'meta'
  if (m.includes('mistral') || m.includes('mixtral')) return 'mistral'
  if (m.includes('grok')) return 'xai'
  if (m.includes('command')) return 'cohere'
  if (m.includes('moonshot') || m.includes('kimi')) return 'moonshot'
  if (m.includes('qwen') || m.includes('通义')) return 'qwen'
  if (m.includes('ernie') || m.includes('文心')) return 'baidu'
  if (m.includes('hunyuan') || m.includes('混元')) return 'tencent'
  if (m.includes('claude') === false && m.includes('amazon.')) return 'aws'
  return ''
}

export function resolveModelCardIcon(
  modelName: string,
  vendor?: string | null,
  iconUrl?: string | null,
): string | undefined {
  const u = (iconUrl || '').trim()
  if (u) return u
  const v = (vendor || '').toLowerCase().trim()
  if (v && VENDOR_SLUG[v]) return simpleIconCDN(VENDOR_SLUG[v])
  const g = guessVendorFromModelName(modelName)
  if (g && VENDOR_SLUG[g]) return simpleIconCDN(VENDOR_SLUG[g])
  return undefined
}

export const VENDOR_PRESET_OPTIONS = [
  { label: 'OpenAI', value: 'openai' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Google', value: 'google' },
  { label: 'DeepSeek', value: 'deepseek' },
  { label: 'Meta', value: 'meta' },
  { label: 'Mistral', value: 'mistral' },
  { label: 'xAI', value: 'xai' },
  { label: 'Cohere', value: 'cohere' },
  { label: 'Moonshot', value: 'moonshot' },
  { label: 'Qwen / 阿里', value: 'qwen' },
  { label: '其他', value: '' },
]

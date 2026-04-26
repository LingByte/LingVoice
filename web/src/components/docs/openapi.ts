export type HttpMethodLower = 'get' | 'post' | 'put' | 'patch' | 'delete' | 'head' | 'options'

export interface OpenApiDoc {
  openapi?: string
  info?: { title?: string; description?: string; version?: string }
  servers?: { url: string; description?: string }[]
  paths?: Record<string, Record<string, unknown>>
  components?: { schemas?: Record<string, unknown> }
}

export interface ResolvedOperation {
  method: string
  path: string
  summary?: string
  description?: string
  operationId?: string
  tags?: string[]
  requestBody?: unknown
  responses?: Record<string, unknown>
}

const METHODS: HttpMethodLower[] = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options']

export function getDefaultServerUrl(spec: OpenApiDoc | null): string {
  if (!spec) return '/v1'
  const u = spec.servers?.[0]?.url?.trim()
  return u && u.length > 0 ? u : '/v1'
}

export function joinServerPath(serverUrl: string, pathKey: string): string {
  const s = serverUrl.replace(/\/$/, '')
  const p = pathKey.startsWith('/') ? pathKey : `/${pathKey}`
  return `${s}${p}`
}

export function findOperation(
  spec: OpenApiDoc | null,
  method: string,
  pathKey: string,
): ResolvedOperation | null {
  if (!spec) return null
  const m = method.toLowerCase()
  const pathObj = spec.paths?.[pathKey]
  if (!pathObj || typeof pathObj !== 'object') return null
  const op = pathObj[m]
  if (!op || typeof op !== 'object') return null
  const o = op as Record<string, unknown>
  return {
    method: m.toUpperCase(),
    path: pathKey,
    summary: typeof o.summary === 'string' ? o.summary : undefined,
    description: typeof o.description === 'string' ? o.description : undefined,
    operationId: typeof o.operationId === 'string' ? o.operationId : undefined,
    tags: Array.isArray(o.tags) ? (o.tags as string[]) : undefined,
    requestBody: o.requestBody,
    responses: typeof o.responses === 'object' && o.responses ? (o.responses as Record<string, unknown>) : undefined,
  }
}

export function listOperations(spec: OpenApiDoc | null): ResolvedOperation[] {
  const out: ResolvedOperation[] = []
  if (!spec) return out
  const paths = spec.paths ?? {}
  for (const pathKey of Object.keys(paths)) {
    const pathObj = paths[pathKey]
    if (!pathObj || typeof pathObj !== 'object') continue
    for (const method of METHODS) {
      const op = (pathObj as Record<string, unknown>)[method]
      if (!op || typeof op !== 'object') continue
      const o = op as Record<string, unknown>
      out.push({
        method: method.toUpperCase(),
        path: pathKey,
        summary: typeof o.summary === 'string' ? o.summary : undefined,
        description: typeof o.description === 'string' ? o.description : undefined,
        operationId: typeof o.operationId === 'string' ? o.operationId : undefined,
        tags: Array.isArray(o.tags) ? (o.tags as string[]) : undefined,
        requestBody: o.requestBody,
        responses:
          typeof o.responses === 'object' && o.responses ? (o.responses as Record<string, unknown>) : undefined,
      })
    }
  }
  return out
}

export function resolveSchemaRef(spec: OpenApiDoc | null, node: unknown): unknown {
  if (!spec || !node || typeof node !== 'object') return node
  const o = node as Record<string, unknown>
  if (typeof o.$ref === 'string') {
    const ref = o.$ref
    const prefix = '#/components/schemas/'
    if (ref.startsWith(prefix)) {
      const name = ref.slice(prefix.length)
      const sch = spec.components?.schemas?.[name]
      return sch !== undefined ? sch : o
    }
    return o
  }
  return node
}

export function getJsonRequestBodySchema(spec: OpenApiDoc | null, op: ResolvedOperation): unknown | null {
  if (!spec) return null
  const rb = op.requestBody as Record<string, unknown> | undefined
  if (!rb) return null
  const content = rb.content as Record<string, Record<string, unknown>> | undefined
  const json = content?.['application/json']
  if (!json?.schema) return null
  return resolveSchemaRef(spec, json.schema)
}

export function getResponseSchema(spec: OpenApiDoc | null, op: ResolvedOperation, status = '200'): unknown | null {
  if (!spec) return null
  const res = op.responses?.[status] as Record<string, unknown> | undefined
  if (!res) return null
  const content = res.content as Record<string, Record<string, unknown>> | undefined
  const json = content?.['application/json']
  if (!json?.schema) return null
  return resolveSchemaRef(spec, json.schema)
}

export interface FlatPropRow {
  name: string
  type: string
  required: boolean
  description: string
}

function typeLabel(s: unknown): string {
  if (!s || typeof s !== 'object') return 'any'
  const o = s as Record<string, unknown>
  const t = o.type
  if (t === 'array') {
    const items = o.items
    return `array<${typeLabel(items)}>`
  }
  if (typeof t === 'string') return t
  if (Array.isArray(o.enum)) return `enum(${o.enum.map(String).join('|')})`
  return 'object'
}

export function flattenSchemaProperties(spec: OpenApiDoc | null, schema: unknown, prefix = ''): FlatPropRow[] {
  if (!schema || typeof schema !== 'object') return []
  const node = spec ? resolveSchemaRef(spec, schema) : schema
  if (!node || typeof node !== 'object') return []
  const o = node as Record<string, unknown>
  const req: string[] = Array.isArray(o.required) ? (o.required as string[]) : []
  const reqSet = new Set(req)
  const props = o.properties as Record<string, unknown> | undefined
  if (!props || o.type === 'array') return []

  const rows: FlatPropRow[] = []
  for (const [key, val] of Object.entries(props)) {
    const resolvedVal = spec ? resolveSchemaRef(spec, val) : val
    const name = prefix ? `${prefix}.${key}` : key
    const child = resolvedVal as Record<string, unknown>
    const desc = typeof child.description === 'string' ? child.description : ''
    rows.push({
      name,
      type: typeLabel(resolvedVal),
      required: reqSet.has(key),
      description: desc,
    })
    if (child.type === 'object' && child.properties && typeof child.properties === 'object') {
      rows.push(...flattenSchemaProperties(spec, resolvedVal, name))
    }
  }
  return rows
}

export function schemaExampleJson(spec: OpenApiDoc | null, schema: unknown): string {
  const ex = buildExample(schema, spec)
  try {
    return JSON.stringify(ex, null, 2)
  } catch {
    return '{}'
  }
}

function buildExample(schema: unknown, spec: OpenApiDoc | null): unknown {
  if (!schema || typeof schema !== 'object') return {}
  const resolved = spec ? resolveSchemaRef(spec, schema) : schema
  if (!resolved || typeof resolved !== 'object') return {}
  const o = resolved as Record<string, unknown>
  if (o.example !== undefined) return o.example
  if (Array.isArray(o.enum) && o.enum.length) return o.enum[0]
  if (o.type === 'string') return ''
  if (o.type === 'number' || o.type === 'integer') return 0
  if (o.type === 'boolean') return false
  if (o.type === 'array') {
    const it = spec ? resolveSchemaRef(spec, o.items) : o.items
    const one = buildExample(it, spec)
    return [one]
  }
  if (o.type === 'object' || o.properties) {
    const props = (o.properties as Record<string, unknown>) || {}
    const req = (Array.isArray(o.required) ? o.required : []) as string[]
    const obj: Record<string, unknown> = {}
    const keys = new Set([...req, ...Object.keys(props)])
    for (const k of keys) {
      if (props[k] !== undefined) {
        const pv = spec ? resolveSchemaRef(spec, props[k]) : props[k]
        obj[k] = buildExample(pv, spec)
      }
    }
    return obj
  }
  return {}
}

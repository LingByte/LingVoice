import { Alert, Typography } from '@arco-design/web-react'
import ReactMarkdown from 'react-markdown'
import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import type { DocBlock } from '@/components/docs/types'
import type { OpenApiDoc } from '@/components/docs/openapi'
import {
  findOperation,
  getDefaultServerUrl,
  getJsonRequestBodySchema,
  joinServerPath,
  schemaExampleJson,
} from '@/components/docs/openapi'
import { DocCodeEditor } from '@/components/docs/DocCodeEditor'
import { OpenApiOperationView } from '@/components/docs/OpenApiOperationView'
import { ApiTryItPanel } from '@/components/docs/ApiTryItPanel'
import { cn } from '@/lib/cn'

const { Text } = Typography

export interface DocBlocksProps {
  blocks: DocBlock[]
  openapi: OpenApiDoc | null
}

export function DocBlocks({ blocks, openapi }: DocBlocksProps) {
  return (
    <div className="doc-blocks space-y-6">
      {blocks.map((b, i) => (
        <DocBlockRenderer key={i} block={b} openapi={openapi} />
      ))}
    </div>
  )
}

function DocBlockRenderer({ block, openapi }: { block: DocBlock; openapi: OpenApiDoc | null }) {
  switch (block.type) {
    case 'markdown':
      return <MarkdownBlock content={block.content} />
    case 'code':
      return <CodeBlock block={block} />
    case 'callout':
      return <CalloutBlock block={block} />
    case 'collapse':
      return <CollapseBlock block={block} openapi={openapi} />
    case 'openapiOperation':
      return <OpenApiBlock block={block} openapi={openapi} />
    case 'playground':
      return <ApiTryItPanel initialMethod="GET" initialPath="/v1/models" initialBody="" />
    default:
      return null
  }
}

function MarkdownBlock({ content }: { content: string }) {
  return (
    <div
      className={cn(
        'doc-md max-w-none text-[15px] leading-relaxed text-[var(--color-text-2)]',
        '[&_h1]:mb-4 [&_h1]:mt-8 [&_h1]:text-[22px] [&_h1]:font-semibold [&_h1]:text-[var(--color-text-1)] [&_h1]:first:mt-0',
        '[&_h2]:mb-3 [&_h2]:mt-7 [&_h2]:text-[18px] [&_h2]:font-semibold [&_h2]:text-[var(--color-text-1)]',
        '[&_h3]:mb-2 [&_h3]:mt-5 [&_h3]:text-[16px] [&_h3]:font-semibold [&_h3]:text-[var(--color-text-1)]',
        '[&_p]:mb-3 [&_p]:last:mb-0',
        '[&_ul]:mb-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:mb-3 [&_ol]:list-decimal [&_ol]:pl-5',
        '[&_li]:mb-1',
        '[&_a]:text-[rgb(var(--primary-6))] [&_a]:underline-offset-2 hover:[&_a]:underline',
        '[&_code]:rounded [&_code]:bg-[var(--color-fill-2)] [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[13px] text-[var(--color-text-1)]',
        '[&_pre]:my-3 [&_pre]:overflow-x-auto [&_pre]:rounded-lg [&_pre]:border [&_pre]:border-[var(--color-border-2)] [&_pre]:bg-[var(--color-fill-1)] [&_pre]:p-3 [&_pre]:font-mono [&_pre]:text-[13px]',
        '[&_blockquote]:my-3 [&_blockquote]:border-l-4 [&_blockquote]:border-[var(--color-border-3)] [&_blockquote]:pl-4 [&_blockquote]:text-[var(--color-text-2)]',
        '[&_hr]:my-8 [&_hr]:border-[var(--color-border-2)]',
      )}
    >
      <ReactMarkdown>{content}</ReactMarkdown>
    </div>
  )
}

function CodeBlock({ block }: { block: Extract<DocBlock, { type: 'code' }> }) {
  return (
    <div>
      {block.title ? (
        <Text className="mb-2 block text-[12px] font-medium text-[var(--color-text-2)]">{block.title}</Text>
      ) : null}
      <DocCodeEditor
        value={block.code}
        language={block.language}
        readOnly={block.readOnly !== false}
        minHeight="140px"
      />
    </div>
  )
}

function CalloutBlock({ block }: { block: Extract<DocBlock, { type: 'callout' }> }) {
  const v = block.variant ?? 'info'
  return (
    <Alert
      type={v === 'error' ? 'error' : v === 'warning' ? 'warning' : v === 'success' ? 'success' : 'info'}
      title={block.title}
      content={<MarkdownBlock content={block.content} />}
    />
  )
}

function CollapseBlock({ block, openapi }: { block: Extract<DocBlock, { type: 'collapse' }>; openapi: OpenApiDoc | null }) {
  const [open, setOpen] = useState(block.defaultOpen ?? false)
  return (
    <div className="overflow-hidden rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)]">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2 px-4 py-3 text-left text-[14px] font-medium text-[var(--color-text-1)] transition-colors hover:bg-[var(--color-fill-2)]"
      >
        <ChevronRight className={cn('h-4 w-4 shrink-0 transition-transform', open && 'rotate-90')} />
        {block.title}
      </button>
      {open ? (
        <div className="space-y-4 border-t border-[var(--color-border-2)] px-4 py-4">
          {block.blocks.map((inner, i) => (
            <DocBlockRenderer key={i} block={inner} openapi={openapi} />
          ))}
        </div>
      ) : null}
    </div>
  )
}

function OpenApiBlock({
  block,
  openapi,
}: {
  block: Extract<DocBlock, { type: 'openapiOperation' }>
  openapi: OpenApiDoc | null
}) {
  if (!openapi) {
    return <Alert type="warning" content="OpenAPI 规范尚未加载，无法渲染该接口块。" />
  }
  const showTry = block.showTry !== false
  const server = getDefaultServerUrl(openapi)
  const fullPath = joinServerPath(server, block.path)
  const tryPath = fullPath.replace(/\{[^}]+\}/g, '1')
  const op = findOperation(openapi, block.method, block.path)
  const bodySchema = op ? getJsonRequestBodySchema(openapi, op) : null
  const generated = bodySchema ? schemaExampleJson(openapi, bodySchema) : '{}'
  const initialBody = block.defaultBody?.trim() ? block.defaultBody : generated

  return (
    <div className="space-y-8">
      <OpenApiOperationView spec={openapi} method={block.method} pathKey={block.path} />
      {showTry ? (
        <div>
          <Text className="mb-3 block text-[13px] font-semibold text-[var(--color-text-1)]">调试</Text>
          <ApiTryItPanel
            initialMethod={block.method}
            initialPath={tryPath.startsWith('/') ? tryPath : `/${tryPath}`}
            initialBody={['POST', 'PUT', 'PATCH', 'DELETE'].includes(block.method.toUpperCase()) ? initialBody : ''}
            compact
          />
        </div>
      ) : null}
    </div>
  )
}

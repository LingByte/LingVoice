import { Alert, Descriptions, Table, Tag, Typography } from '@arco-design/web-react'
import type { OpenApiDoc } from '@/components/docs/openapi'
import {
  findOperation,
  flattenSchemaProperties,
  getDefaultServerUrl,
  getJsonRequestBodySchema,
  getResponseSchema,
  joinServerPath,
  schemaExampleJson,
} from '@/components/docs/openapi'
import { DocCodeEditor } from '@/components/docs/DocCodeEditor'
import { getApiBaseURL } from '@/config/apiConfig'

const { Title, Paragraph, Text } = Typography

export interface OpenApiOperationViewProps {
  spec: OpenApiDoc
  method: string
  pathKey: string
}

export function OpenApiOperationView({ spec, method, pathKey }: OpenApiOperationViewProps) {
  const op = findOperation(spec, method, pathKey)
  if (!op) {
    return <Alert type="error" content={`未在 openapi.json 中找到 ${method.toUpperCase()} ${pathKey}`} />
  }

  const server = getDefaultServerUrl(spec)
  const fullPath = joinServerPath(server, pathKey)
  const bodySchema = getJsonRequestBodySchema(spec, op)
  const resSchema = getResponseSchema(spec, op, '200')
  const reqExample = bodySchema ? schemaExampleJson(spec, bodySchema) : ''
  const resExample = resSchema ? schemaExampleJson(spec, resSchema) : ''
  const reqRows = bodySchema ? flattenSchemaProperties(spec, bodySchema) : []
  const base = getApiBaseURL()

  return (
    <div className="space-y-8">
      <div className="flex flex-wrap items-start gap-3">
        <Tag color="arcoblue" size="large" className="!m-0 !px-2.5 !text-[13px] !font-semibold">
          {op.method}
        </Tag>
        <div className="min-w-0 flex-1">
          <Title heading={4} className="!mb-1 !mt-0 !text-[20px]">
            {op.summary ?? op.operationId ?? '接口'}
          </Title>
          <Text code className="text-[13px]">
            {fullPath}
          </Text>
          {op.operationId ? (
            <Paragraph className="!mb-0 !mt-2 !text-[12px] text-[var(--color-text-3)]">
              operationId: <Text code>{op.operationId}</Text>
            </Paragraph>
          ) : null}
        </div>
      </div>

      {op.description ? (
        <Paragraph className="!mb-0 !text-[14px] leading-relaxed text-[var(--color-text-2)]">{op.description}</Paragraph>
      ) : null}

      <section>
        <Title heading={6} className="!mb-3 !text-[13px] !font-semibold !uppercase !tracking-wide !text-[var(--color-text-3)]">
          请求
        </Title>
        <Descriptions
          column={1}
          size="small"
          data={[
            { label: '完整 URL', value: <Text code>{`${base}${fullPath}`}</Text> },
            { label: 'Content-Type', value: op.method === 'GET' ? '—' : 'application/json' },
          ]}
          className="!bg-[var(--color-fill-1)]"
        />
        {reqRows.length > 0 ? (
          <div className="mt-4 overflow-hidden rounded-lg border border-[var(--color-border-2)]">
            <Table
              size="small"
              pagination={false}
              rowKey="name"
              border={{ wrapper: true, cell: true }}
              columns={[
                { title: '字段', dataIndex: 'name', width: 200 },
                { title: '类型', dataIndex: 'type', width: 140 },
                {
                  title: '必填',
                  dataIndex: 'required',
                  width: 72,
                  render: (v: boolean) => (v ? <Tag color="red">必填</Tag> : <Text type="secondary">可选</Text>),
                },
                { title: '说明', dataIndex: 'description' },
              ]}
              data={reqRows}
            />
          </div>
        ) : (
          <Paragraph type="secondary" className="!mt-3 !mb-0 !text-[13px]">
            无 JSON 请求体或无法从 schema 展开字段表。
          </Paragraph>
        )}
        {reqExample ? (
          <div className="mt-4">
            <Text className="mb-2 block text-[12px] font-medium text-[var(--color-text-2)]">示例请求体</Text>
            <DocCodeEditor value={reqExample} language="json" readOnly minHeight="140px" />
          </div>
        ) : null}
      </section>

      <section>
        <Title heading={6} className="!mb-3 !text-[13px] !font-semibold !uppercase !tracking-wide !text-[var(--color-text-3)]">
          响应（200）
        </Title>
        {resExample ? (
          <DocCodeEditor value={resExample} language="json" readOnly minHeight="140px" />
        ) : (
          <Paragraph type="secondary" className="!mb-0 !text-[13px]">
            未声明 application/json 的 200 响应 schema。
          </Paragraph>
        )}
      </section>
    </div>
  )
}

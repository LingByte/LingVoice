import { Button, Message, Space, Spin, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import { AdminOnly } from '@/components/AdminOnly'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'
import {
  getKnowledgeDocument,
  getKnowledgeDocumentText,
  isMilvusVectorProvider,
  updateKnowledgeDocumentText,
  type KnowledgeDocumentRow,
} from '@/api/knowledgeAdmin'

const { Title, Paragraph, Text } = Typography

export function KnowledgeDocumentDetailPage() {
  const navigate = useNavigate()
  const { id } = useParams()
  const docID = id || ''

  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [doc, setDoc] = useState<KnowledgeDocumentRow | null>(null)
  const [vectorProvider, setVectorProvider] = useState<string | undefined>()
  const [textURL, setTextURL] = useState('')
  const [markdown, setMarkdown] = useState('')

  const nsIsMilvus = useMemo(() => isMilvusVectorProvider(vectorProvider), [vectorProvider])

  const load = useCallback(async () => {
    if (!docID) return
    setLoading(true)
    try {
      const [d, t] = await Promise.all([getKnowledgeDocument(docID), getKnowledgeDocumentText(docID)])
      setDoc(d.document)
      setVectorProvider(d.vector_provider)
      setTextURL(t.url || '')
      setMarkdown(t.markdown || '')
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [docID])

  useEffect(() => {
    void load()
  }, [load])

  const preview = useMemo(() => markdown, [markdown])

  const save = async () => {
    if (!docID) return
    if (!markdown.trim()) {
      Message.error('内容不能为空')
      return
    }
    setSaving(true)
    try {
      const res = await updateKnowledgeDocumentText(docID, markdown)
      Message.success('已提交后台处理（请稍后刷新查看结果）')
      setDoc(res.document)
      setTextURL(res.document.text_url || '')
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <AdminOnly title="文档详情">
      <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        <div className="mb-4 flex flex-wrap items-start gap-3">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-3">
            <Button className="shrink-0" onClick={() => navigate(-1)}>
              返回
            </Button>
            <div className="min-w-0">
              <Title heading={5} className="!m-0">
                文档详情
              </Title>
              <Paragraph type="secondary" className="!mb-0 !mt-1">
                <Space wrap>
                  <span>
                    id: <Text code>{doc?.id || '-'}</Text>
                  </span>
                  <span>
                    namespace: <Text code>{doc?.namespace || '-'}</Text>
                  </span>
                  <span>
                    title: <Text code>{doc?.title || '-'}</Text>
                  </span>
                  {nsIsMilvus && <Text type="secondary">向量后端：Milvus</Text>}
                </Space>
              </Paragraph>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <Button className="shrink-0" loading={loading} onClick={() => void load()}>
              刷新
            </Button>
            <Button
              className="shrink-0"
              type="primary"
              loading={saving}
              onClick={() => void save()}
              disabled={loading}
            >
              保存
            </Button>
          </div>
        </div>

        <Spin loading={loading}>
          <div className="mb-3 min-w-0 rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] px-3 py-2 text-[13px]">
            <div className="mb-1 text-[var(--color-text-2)]">解析文本 URL（md）</div>
            {textURL ? (
              <EllipsisCopyText text={textURL} maxWidth={980} copiedTip="URL 已复制" />
            ) : (
              <Text type="secondary">暂无</Text>
            )}
          </div>

          <div className="grid min-h-0 w-full min-w-0 flex-1 grid-cols-2 gap-3">
            <div className="min-h-0 min-w-0 overflow-hidden rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-2">
              <div className="mb-2 px-2 text-[13px] text-[var(--color-text-2)]">编辑（Markdown）</div>
              <textarea
                className="h-[75vh] w-full min-w-0 resize-none rounded-md border border-[var(--color-border-2)] bg-[var(--color-bg-1)] p-2 text-[13px] outline-none read-only:cursor-default read-only:bg-[var(--color-fill-2)]"
                value={markdown}
                readOnly={false}
                onChange={(e) => setMarkdown(e.target.value)}
              />
            </div>
            <div className="min-h-0 min-w-0 overflow-hidden rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-3">
              <div className="mb-2 text-[13px] text-[var(--color-text-2)]">预览</div>
              <div className="h-[75vh] min-h-0 overflow-auto">
                <div className="prose max-w-none text-[13px]">
                  <ReactMarkdown>{preview}</ReactMarkdown>
                </div>
              </div>
            </div>
          </div>
        </Spin>
      </div>
    </AdminOnly>
  )
}


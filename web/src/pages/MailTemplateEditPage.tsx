import {
  Button,
  Form,
  Input,
  Message,
  Select,
  Switch,
  Typography,
} from '@arco-design/web-react'
import CodeMirror from '@uiw/react-codemirror'
import { vscodeLight } from '@uiw/codemirror-theme-vscode'
import { html } from '@codemirror/lang-html'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  createMailTemplate,
  getMailTemplate,
  translateMailTemplate,
  updateMailTemplate,
} from '@/api/mailAdmin'

const { Title, Paragraph, Text } = Typography
const FormItem = Form.Item

const LOCALE_OPTIONS = [
  { label: '默认（不区分语言）', value: '' },
  { label: '简体中文 zh-CN', value: 'zh-CN' },
  { label: '繁体中文 zh-TW', value: 'zh-TW' },
  { label: 'English en', value: 'en' },
  { label: 'English en-US', value: 'en-US' },
  { label: '日本語 ja', value: 'ja' },
]

const LOCALE_OPTIONS_FOR_SOURCE = LOCALE_OPTIONS.filter((o) => o.value !== '')

const cmExtensions = [html(), vscodeLight]

export function MailTemplateEditPage() {
  const { templateId } = useParams<{ templateId: string }>()
  const navigate = useNavigate()
  const isNew = templateId === 'new'
  const [loading, setLoading] = useState(!isNew)
  const [htmlBody, setHtmlBody] = useState('')
  const [fromLocale, setFromLocale] = useState('zh-CN')
  const [translating, setTranslating] = useState(false)
  const [form] = Form.useForm<{
    code: string
    name: string
    description: string
    locale: string
    enabled: boolean
    htmlBody: string
  }>()

  const previewDoc = useMemo(() => {
    const body = htmlBody.trim() || '<p style="color:#888;font:14px system-ui">暂无 HTML 内容</p>'
    return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:12px;font:14px/1.5 system-ui,-apple-system,sans-serif;color:#1a1a1a}</style></head><body>${body}</body></html>`
  }, [htmlBody])

  useEffect(() => {
    if (isNew) {
      form.setFieldsValue({
        code: '',
        name: '',
        description: '',
        locale: '',
        enabled: true,
        htmlBody: '',
      })
      setHtmlBody('')
      setLoading(false)
      return
    }
    const id = templateId
    if (!id || id === 'new') {
      Message.error('无效的 id')
      navigate('/notify/mail-templates')
      return
    }
    let cancelled = false
    ;(async () => {
      setLoading(true)
      try {
        const row = await getMailTemplate(id)
        if (cancelled) return
        form.setFieldsValue({
          code: row.code,
          name: row.name,
          description: row.description ?? '',
          locale: row.locale ?? '',
          enabled: row.enabled,
          htmlBody: row.htmlBody ?? '',
        })
        setHtmlBody(row.htmlBody ?? '')
      } catch (e) {
        Message.error(e instanceof Error ? e.message : '加载失败')
        navigate('/notify/mail-templates')
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [form, isNew, navigate, templateId])

  const runTranslate = async () => {
    const toLocale = (form.getFieldValue('locale') as string | undefined)?.trim() ?? ''
    if (!toLocale) {
      Message.warning('请先在「模版语言」中选择目标语种，再使用翻译')
      return
    }
    if (fromLocale === toLocale) {
      Message.info('原文语言与模版语言相同，无需翻译')
      return
    }
    let v: { name: string; htmlBody: string; description: string }
    try {
      const p = await form.validate(['name', 'htmlBody', 'description'])
      v = {
        name: p.name ?? '',
        htmlBody: p.htmlBody ?? '',
        description: p.description ?? '',
      }
    } catch {
      return
    }
    setTranslating(true)
    try {
      const out = await translateMailTemplate({
        fromLocale,
        toLocale,
        name: v.name ?? '',
        htmlBody: v.htmlBody ?? '',
        description: v.description ?? '',
      })
      form.setFieldsValue({
        name: out.name,
        htmlBody: out.htmlBody,
        description: out.description,
      })
      setHtmlBody(out.htmlBody ?? '')
      Message.success('已翻译并写入表单（请检查 HTML 占位符与标签是否完整）')
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '翻译失败')
    } finally {
      setTranslating(false)
    }
  }

  const submit = async () => {
    try {
      const v = await form.validate()
      if (isNew) {
        await createMailTemplate({
          code: v.code,
          name: v.name,
          htmlBody: v.htmlBody,
          description: v.description,
          locale: v.locale || undefined,
          enabled: v.enabled,
        })
        Message.success('已创建')
      } else {
        await updateMailTemplate(templateId!, {
          name: v.name,
          htmlBody: v.htmlBody,
          description: v.description,
          locale: v.locale || undefined,
          enabled: v.enabled,
        })
        Message.success('已保存')
      }
      navigate('/notify/mail-templates')
    } catch (e) {
      if (e instanceof Error && e.message) Message.error(e.message)
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col bg-[var(--color-fill-1)]">
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-[var(--color-border-2)] px-5 py-3">
        <Button size="small" onClick={() => navigate('/notify/mail-templates')}>
          返回列表
        </Button>
        <Title heading={6} className="!m-0">
          {isNew ? '新建邮件模版' : '编辑邮件模版'}
        </Title>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden p-4">
        <Paragraph type="secondary" className="!mb-3 !mt-0 max-w-4xl text-[12px]">
          只需维护 HTML；保存时后端会生成纯文本 textBody 并解析占位符，前端不提交 textBody。邮件主题由发送侧业务自行决定。机器翻译会处理名称、说明与 HTML（MyMemory；保存前请核对占位符）。
        </Paragraph>

        <div className="mx-auto grid h-[calc(100%-3rem)] max-w-[1400px] grid-cols-1 gap-4 lg:grid-cols-2 lg:gap-5">
          <div className="flex min-h-0 flex-col overflow-hidden rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-4 shadow-sm">
            <Text className="mb-2 block text-[13px] font-medium">编辑</Text>
            <div className="min-h-0 flex-1 overflow-y-auto pr-1">
              <Form
                form={form}
                layout="vertical"
                disabled={loading}
                onValuesChange={(_, all) => {
                  if (typeof all.htmlBody === 'string') setHtmlBody(all.htmlBody)
                }}
              >
                <FormItem
                  label="Code（业务键）"
                  field="code"
                  rules={[{ required: true }]}
                  extra="新建后不可修改"
                >
                  <Input placeholder="如 welcome、verify_code" disabled={!isNew} />
                </FormItem>
                <FormItem label="名称" field="name" rules={[{ required: true }]}>
                  <Input />
                </FormItem>
                <FormItem
                  label="机器翻译"
                  extra="仅调用翻译接口时使用，不会单独存库。请先选好上面的「模版语言」。"
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <Text className="shrink-0 text-[13px] text-[var(--color-text-2)]">当前撰写语言</Text>
                    <Select
                      className="min-w-[10rem] max-w-[14rem] flex-1 sm:flex-initial"
                      options={LOCALE_OPTIONS_FOR_SOURCE}
                      value={fromLocale}
                      disabled={loading}
                      onChange={(v) => setFromLocale(v)}
                      placeholder="选择语言"
                    />
                    <Button
                      type="outline"
                      size="small"
                      className="shrink-0"
                      loading={translating}
                      disabled={loading}
                      onClick={() => void runTranslate()}
                    >
                      译成模版语言
                    </Button>
                  </div>
                </FormItem>
                <div className="grid grid-cols-1 gap-x-3 sm:grid-cols-2">
                  <FormItem
                    label="模版语言"
                    field="locale"
                    extra="同一code可建多语种模版, 选「默认」表示不区分"
                  >
                    <Select options={LOCALE_OPTIONS} placeholder="选择模版语言" allowClear />
                  </FormItem>
                  <FormItem label="启用" field="enabled" triggerPropName="checked">
                    <Switch />
                  </FormItem>
                </div>
                <FormItem label="说明" field="description">
                  <Input placeholder="可选，内部备注" />
                </FormItem>
                <FormItem
                  label="HTML 正文"
                  field="htmlBody"
                  rules={[{ required: true, message: '请填写 HTML' }]}
                >
                  <CodeMirror
                    className="mail-template-codemirror overflow-hidden rounded border border-[var(--color-border-2)] text-[13px]"
                    height="min(50vh, 420px)"
                    theme="none"
                    extensions={cmExtensions}
                    basicSetup={{
                      lineNumbers: true,
                      foldGutter: true,
                      bracketMatching: true,
                      closeBrackets: true,
                    }}
                    placeholder="<p>Hello {{.Name}}</p>"
                  />
                </FormItem>
              </Form>
            </div>
            <div className="mt-3 flex shrink-0 justify-end gap-2 border-t border-[var(--color-border-2)] pt-3">
              <Button onClick={() => navigate('/notify/mail-templates')}>取消</Button>
              <Button type="primary" loading={loading} onClick={() => void submit()}>
                保存
              </Button>
            </div>
          </div>

          <div className="flex min-h-[320px] min-w-0 flex-col overflow-hidden rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-4 shadow-sm lg:min-h-0">
            <Text className="mb-2 block text-[13px] font-medium">预览（390×667，与常见手机视口接近）</Text>
            <div className="flex min-h-0 flex-1 items-start justify-center overflow-auto py-1">
              <div
                className="box-border w-[390px] max-w-full shrink-0 overflow-hidden rounded border border-[var(--color-border-2)] bg-[var(--color-bg-1)] shadow-sm"
                style={{ height: 'min(667px, calc(100vh - 260px))' }}
              >
                <iframe
                  title="html-preview"
                  className="h-full w-full border-0 bg-white"
                  sandbox="allow-same-origin"
                  srcDoc={previewDoc}
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

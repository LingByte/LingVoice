import {
  Button,
  Form,
  Input,
  InputNumber,
  Message,
  Radio,
  Select,
  Switch,
  Typography,
} from '@arco-design/web-react'
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  createNotificationChannel,
  getNotificationChannelDetail,
  updateNotificationChannel,
  type NotificationChannelUpsertBody,
} from '@/api/mailAdmin'

const { Title } = Typography
const FormItem = Form.Item
const TextArea = Input.TextArea

export function NotificationChannelEditPage() {
  const { channelId } = useParams<{ channelId: string }>()
  const navigate = useNavigate()
  const isNew = channelId === 'new'
  const [loading, setLoading] = useState(!isNew)
  const [form] = Form.useForm()

  useEffect(() => {
    if (isNew) {
      form.setFieldsValue({
        channelType: 'email',
        driver: 'smtp',
        name: '',
        sortOrder: 0,
        enabled: true,
        remark: '',
        smtpHost: '',
        smtpPort: 587,
        smtpUsername: '',
        smtpPassword: '',
        smtpFrom: '',
        sendcloudApiUser: '',
        sendcloudApiKey: '',
        sendcloudFrom: '',
        fromDisplayName: '',
        smsProvider: 'yunpian',
        smsConfigJson: JSON.stringify({ apiKey: '' }, null, 2),
      })
      setLoading(false)
      return
    }
    const id = channelId
    if (!id || id === 'new') {
      Message.error('无效的 id')
      navigate('/notify/channels')
      return
    }
    let cancelled = false
    ;(async () => {
      setLoading(true)
      try {
        const { channel, emailForm, smsForm } = await getNotificationChannelDetail(id)
        if (cancelled) return
        if (channel.type === 'email') {
          const d = emailForm?.driver === 'sendcloud' ? 'sendcloud' : 'smtp'
          form.setFieldsValue({
            channelType: 'email',
            driver: d,
            name: channel.name,
            sortOrder: channel.sortOrder,
            enabled: channel.enabled,
            remark: channel.remark ?? '',
            smtpHost: emailForm?.smtpHost ?? '',
            smtpPort: emailForm?.smtpPort ?? 587,
            smtpUsername: emailForm?.smtpUsername ?? '',
            smtpPassword: '',
            smtpFrom: emailForm?.smtpFrom ?? '',
            sendcloudApiUser: emailForm?.sendcloudApiUser ?? '',
            sendcloudApiKey: '',
            sendcloudFrom: emailForm?.sendcloudFrom ?? '',
            fromDisplayName: emailForm?.fromDisplayName ?? '',
          })
        } else if (channel.type === 'sms') {
          const provider = String(smsForm?.provider || 'yunpian')
          const cfg = (smsForm?.config || {}) as Record<string, unknown>
          form.setFieldsValue({
            channelType: 'sms',
            name: channel.name,
            sortOrder: channel.sortOrder,
            enabled: channel.enabled,
            remark: channel.remark ?? '',
            smsProvider: provider,
            smsConfigJson: JSON.stringify(cfg ?? {}, null, 2),
          })
        } else {
          Message.warning(`暂不支持编辑渠道类型: ${channel.type}`)
          navigate('/notify/channels')
          return
        }
      } catch (e) {
        Message.error(e instanceof Error ? e.message : '加载失败')
        navigate('/notify/channels')
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [channelId, form, isNew, navigate])

  const submit = async () => {
    try {
      const v = await form.validate() as Record<string, unknown>
      const channelType = String(v.channelType || 'email')
      let body: NotificationChannelUpsertBody
      if (channelType === 'email') {
        const driver = String(v.driver || 'smtp') as 'smtp' | 'sendcloud'
        if (driver === 'smtp' && isNew && !String(v.smtpPassword || '').trim()) {
          Message.error('新建 SMTP 渠道请填写密码')
          return
        }
        if (driver === 'sendcloud' && isNew && !String(v.sendcloudApiKey || '').trim()) {
          Message.error('新建 SendCloud 渠道请填写 API Key')
          return
        }
        body = {
          channelType: 'email',
          driver,
          name: String(v.name || ''),
          sortOrder: Number(v.sortOrder || 0),
          enabled: Boolean(v.enabled),
          remark: String(v.remark || ''),
          smtpHost: String(v.smtpHost || ''),
          smtpPort: Number(v.smtpPort || 587),
          smtpUsername: String(v.smtpUsername || ''),
          smtpPassword: String(v.smtpPassword || ''),
          smtpFrom: String(v.smtpFrom || ''),
          sendcloudApiUser: String(v.sendcloudApiUser || ''),
          sendcloudApiKey: String(v.sendcloudApiKey || ''),
          sendcloudFrom: String(v.sendcloudFrom || ''),
          fromDisplayName: String(v.fromDisplayName ?? '').trim(),
        }
      } else {
        const smsProvider = String(v.smsProvider || 'yunpian')
        let cfg: Record<string, unknown> = {}
        try {
          const raw = String(v.smsConfigJson || '').trim()
          cfg = raw ? (JSON.parse(raw) as Record<string, unknown>) : {}
        } catch (e) {
          Message.error('SMS 配置 JSON 解析失败')
          return
        }
        body = {
          channelType: 'sms',
          name: String(v.name || ''),
          sortOrder: Number(v.sortOrder || 0),
          enabled: Boolean(v.enabled),
          remark: String(v.remark || ''),
          smsProvider,
          smsConfig: cfg,
        }
      }
      if (isNew) {
        await createNotificationChannel(body)
        Message.success('已创建')
      } else {
        await updateNotificationChannel(channelId!, body)
        Message.success('已保存')
      }
      navigate('/notify/channels')
    } catch (e) {
      if (e instanceof Error && e.message) Message.error(e.message)
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col bg-[var(--color-fill-1)]">
      <div className="flex shrink-0 items-center gap-3 border-b border-[var(--color-border-2)] px-5 py-3">
        <Button size="small" onClick={() => navigate('/notify/channels')}>
          返回列表
        </Button>
        <Title heading={6} className="!m-0">
          {isNew ? '新建渠道' : '编辑渠道'}
        </Title>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="mx-auto w-full max-w-xl rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-2 shadow-sm">
          <Form form={form} layout="vertical" disabled={loading}>
            <FormItem label="渠道类型" field="channelType" rules={[{ required: true }]}>
              <Radio.Group type="button" size="small">
                <Radio value="email">邮件</Radio>
                <Radio value="sms">短信</Radio>
              </Radio.Group>
            </FormItem>

            <FormItem noStyle shouldUpdate>
              {(values) =>
                values.channelType === 'email' ? (
                  <FormItem label="发送方式" field="driver" rules={[{ required: true }]}>
                    <Radio.Group type="button" size="small">
                      <Radio value="smtp">SMTP</Radio>
                      <Radio value="sendcloud">SendCloud</Radio>
                    </Radio.Group>
                  </FormItem>
                ) : null
              }
            </FormItem>

            <FormItem label="显示名称" field="name" rules={[{ required: true }]}>
              <Input placeholder="列表展示名，并写入发送配置" />
            </FormItem>

            <FormItem noStyle shouldUpdate>
              {(values) =>
                values.channelType === 'email' ? (
                  <FormItem
                    label="发件人显示名（可选）"
                    field="fromDisplayName"
                    extra="收件人邮箱客户端「发件人」旁看到的名称，例如：XXX公司。From 请填已通过域名验证的邮箱地址。"
                  >
                    <Input placeholder="邮件显示" />
                  </FormItem>
                ) : null
              }
            </FormItem>

            <div className="grid grid-cols-2 gap-x-3">
              <FormItem label="排序（越小越优先）" field="sortOrder">
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormItem>
              <FormItem label="启用" field="enabled" triggerPropName="checked">
                <Switch />
              </FormItem>
            </div>

            <FormItem noStyle shouldUpdate>
              {(values) =>
                values.channelType === 'email' ? (
                  values.driver === 'sendcloud' ? (
                    <div className="space-y-0">
                      <FormItem label="API User" field="sendcloudApiUser" rules={[{ required: true }]}>
                        <Input />
                      </FormItem>
                      <FormItem
                        label="API Key"
                        field="sendcloudApiKey"
                        rules={isNew ? [{ required: true, message: '请填写 API Key' }] : undefined}
                        extra={isNew ? undefined : '留空则不修改已保存的 Key'}
                      >
                        <Input.Password placeholder={isNew ? '必填' : '可选'} />
                      </FormItem>
                      <FormItem label="发件地址 From" field="sendcloudFrom" rules={[{ required: true }]}>
                        <Input />
                      </FormItem>
                    </div>
                ) : (
                  <div className="space-y-0">
                    <div className="grid grid-cols-2 gap-x-3">
                      <FormItem label="主机" field="smtpHost" rules={[{ required: true }]}>
                        <Input placeholder="smtp.example.com" />
                      </FormItem>
                      <FormItem label="端口" field="smtpPort" rules={[{ required: true }]}>
                        <InputNumber min={1} max={65535} style={{ width: '100%' }} />
                      </FormItem>
                    </div>
                    <div className="grid grid-cols-2 gap-x-3">
                      <FormItem label="用户名" field="smtpUsername">
                        <Input />
                      </FormItem>
                      <FormItem
                        label="密码"
                        field="smtpPassword"
                        rules={isNew ? [{ required: true, message: '请填写密码' }] : undefined}
                        extra={isNew ? undefined : '留空则不修改已保存的密码'}
                      >
                        <Input.Password placeholder={isNew ? '必填' : '可选'} />
                      </FormItem>
                    </div>
                    <FormItem label="发件地址 From" field="smtpFrom" rules={[{ required: true }]}>
                      <Input />
                    </FormItem>
                  </div>
                )
                ) : (
                  <div className="space-y-0">
                    <FormItem label="短信 Provider" field="smsProvider" rules={[{ required: true }]}>
                      <Select
                        options={[
                          { label: '云片 Yunpian', value: 'yunpian' },
                          { label: '螺丝帽 Luosimao', value: 'luosimao' },
                          { label: 'Twilio', value: 'twilio' },
                          { label: '互亿 Huyi', value: 'huyi' },
                          { label: '聚合 Juhe', value: 'juhe' },
                          { label: '创蓝 Chuanglan', value: 'chuanglan' },
                          { label: 'Submail', value: 'submail' },
                          { label: '阿里云 Aliyun', value: 'aliyun' },
                          { label: '腾讯云 Tencent', value: 'tencent' },
                          { label: '华为云 Huawei', value: 'huawei' },
                          { label: '融云 Rongcloud', value: 'rongcloud' },
                          { label: '网易云信 Netease', value: 'netease' },
                          { label: '容联云通讯 Yuntongxun', value: 'yuntongxun' },
                        ]}
                      />
                    </FormItem>

                    <FormItem
                      label="SMS 配置（JSON）"
                      field="smsConfigJson"
                      extra="按 provider 填对应字段；编辑时留空字段可在后端保留 secret（字符串空值会触发保留旧值）。"
                      rules={[{ required: true, message: '请填写配置 JSON' }]}
                    >
                      <TextArea autoSize={{ minRows: 8, maxRows: 18 }} placeholder='例如：{ "apiKey": "xxx" }' />
                    </FormItem>
                  </div>
                )
              }
            </FormItem>

            <FormItem label="备注" field="remark">
              <Input />
            </FormItem>

            <div className="flex justify-end gap-2 pt-2">
              <Button onClick={() => navigate('/notify/channels')}>取消</Button>
              <Button type="primary" loading={loading} onClick={() => void submit()}>
                保存
              </Button>
            </div>
          </Form>
        </div>
      </div>
    </div>
  )
}

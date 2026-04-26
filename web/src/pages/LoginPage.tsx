import { useState, useEffect } from 'react'
import {
  Button,
  Form,
  Input,
  Message,
  Select,
  Tabs,
  Typography,
} from '@arco-design/web-react'
import { Link, useNavigate } from 'react-router-dom'
import {
  loginWithEmailCode,
  loginWithPassword,
  sendEmailLoginCode,
} from '@/api/auth'
import { persistAuthSession } from '@/stores/authStore'

const { Title, Text } = Typography
const TabPane = Tabs.TabPane

const LANG_OPTIONS = [
  { label: '简体中文', value: 'zh-CN' },
  { label: 'English', value: 'en-US' },
]

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function LoginPage() {
  const navigate = useNavigate()
  const [pwdForm] = Form.useForm()
  const [emailForm] = Form.useForm()
  const [activeTab, setActiveTab] = useState('password')
  const [codeSending, setCodeSending] = useState(false)
  const [codeLoggingIn, setCodeLoggingIn] = useState(false)
  const [countdown, setCountdown] = useState(0)

  const onPasswordSubmit = async () => {
    const v = await pwdForm.validate().catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    const password = String(v.password ?? '')
    if (!EMAIL_RE.test(email)) {
      Message.error('请输入有效邮箱')
      return
    }
    try {
      const session = await loginWithPassword(email, password)
      persistAuthSession(session)
      Message.success('登录成功')
      navigate('/', { replace: true })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '登录失败')
    }
  }

  const onSendCode = async () => {
    const v = await emailForm.validate(['email']).catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    if (!EMAIL_RE.test(email)) {
      Message.error('请输入有效邮箱')
      return
    }
    setCodeSending(true)
    try {
      await sendEmailLoginCode(email)
      Message.success('若该邮箱已注册，将收到验证码邮件，请查收邮箱')
      setCountdown(60)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '发送失败')
    } finally {
      setCodeSending(false)
    }
  }

  useEffect(() => {
    if (countdown > 0) {
      const timer = setTimeout(() => setCountdown(countdown - 1), 1000)
      return () => clearTimeout(timer)
    }
  }, [countdown])

  const onEmailCodeLogin = async () => {
    const v = await emailForm.validate().catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    const code = String(v.code ?? '').trim()
    if (!EMAIL_RE.test(email)) {
      Message.error('请输入有效邮箱')
      return
    }
    if (code.replace(/\D/g, '').length < 6) {
      Message.error('请输入邮件中的 6 位验证码')
      return
    }
    setCodeLoggingIn(true)
    try {
      const session = await loginWithEmailCode(email, code)
      persistAuthSession(session)
      Message.success('登录成功')
      navigate('/', { replace: true })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '验证码错误或已过期')
    } finally {
      setCodeLoggingIn(false)
    }
  }

  return (
    <div className="relative flex flex-col h-screen h-[100dvh] max-h-screen max-h-[100dvh] overflow-hidden">
      <div className="absolute inset-0 bg-[url('/background.png')] bg-center bg-cover" aria-hidden />
      <div className="absolute inset-0 bg-gradient-to-br from-purple-500/15 via-pink-400/10 to-blue-500/15 backdrop-blur-sm" />

      <div className="absolute right-6 top-6 z-10">
        <Select
          size="small"
          defaultValue="zh-CN"
          options={LANG_OPTIONS}
          className="w-32"
        />
      </div>

      <div className="relative z-10 flex flex-1 min-h-0 flex-col items-center justify-center p-8 pt-8 pb-4 gap-6">
        <div className="flex items-center gap-4 animate-[fadeIn_0.6s_ease-out]">
          <img
            src="/logo.png"
            alt=""
            width={48}
            height={48}
            className="rounded-2xl shadow-lg shadow-purple-500/40 transition-transform hover:scale-110"
          />
          <Title heading={4} className="!m-0 text-[var(--color-text-1)]">
            LingVoice
          </Title>
        </div>

        <div className="w-full max-w-[420px] rounded-3xl border border-white/40 bg-white/95 p-8 pb-6 shadow-[0_1px_0_rgba(255,255,255,0.6)_inset,0_32px_80px_-24px_rgba(0,0,0,0.2),0_12px_32px_-12px_rgba(0,0,0,0.1)] backdrop-blur-xl animate-[fadeIn_0.5s_ease-out]">
          <Tabs
            activeTab={activeTab}
            onChange={setActiveTab}
            className="mb-2"
          >
            <TabPane key="password" title="账号密码">
              <Form
                form={pwdForm}
                layout="vertical"
                requiredSymbol={false}
                className="mb-5 last:mb-4"
                onSubmit={onPasswordSubmit}
              >
                <Form.Item
                  field="email"
                  label="邮箱"
                  rules={[{ required: true, message: '请输入邮箱' }]}
                >
                  <Input
                    size="large"
                    placeholder="请输入您的邮箱"
                    autoComplete="email"
                    className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
                  />
                </Form.Item>
                <Form.Item
                  field="password"
                  label="密码"
                  rules={[{ required: true, message: '请输入密码' }]}
                >
                  <Input.Password
                    size="large"
                    placeholder="密码"
                    autoComplete="current-password"
                    className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
                  />
                </Form.Item>

                <Button
                  type="primary"
                  htmlType="submit"
                  long
                  size="large"
                  className="!h-12 !mb-4 !rounded-xl !text-base !font-semibold !border-none !bg-gradient-to-r !from-purple-500 !via-purple-600 !to-purple-700 !shadow-[0_12px_32px_-12px_rgba(139,92,246,0.5)] hover:!-translate-y-0.5 hover:!shadow-[0_16px_40px_-12px_rgba(139,92,246,0.6)] active:!translate-y-0 active:!shadow-[0_8px_24px_-8px_rgba(139,92,246,0.5)] transition-all"
                >
                  登录
                </Button>
              </Form>
            </TabPane>

            <TabPane key="email" title="邮箱登录">
              <Form
                form={emailForm}
                layout="vertical"
                requiredSymbol={false}
                className="mb-5 last:mb-4"
              >
                <Form.Item
                  field="email"
                  label="邮箱"
                  rules={[{ required: true, message: '请输入邮箱' }]}
                >
                  <Input
                    size="large"
                    placeholder="请输入您的邮箱"
                    autoComplete="email"
                    className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
                  />
                </Form.Item>
                <Form.Item
                  field="code"
                  label="验证码"
                  rules={[{ required: true, message: '请输入邮件中的验证码' }]}
                >
                  <Input
                    size="large"
                    placeholder="6 位数字"
                    maxLength={16}
                    autoComplete="one-time-code"
                    className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
                    addAfter={
                      <Button
                        type="text"
                        size="small"
                        disabled={countdown > 0 || codeSending}
                        loading={codeSending}
                        onClick={onSendCode}
                        className="px-3"
                      >
                        {countdown > 0 ? `${countdown}s` : '获取验证码'}
                      </Button>
                    }
                  />
                </Form.Item>
                <Button
                  type="primary"
                  long
                  size="large"
                  className="!h-12 !mb-4 !rounded-xl !text-base !font-semibold !border-none !bg-gradient-to-r !from-purple-500 !via-purple-600 !to-purple-700 !shadow-[0_12px_32px_-12px_rgba(139,92,246,0.5)] hover:!-translate-y-0.5 hover:!shadow-[0_16px_40px_-12px_rgba(139,92,246,0.6)] active:!translate-y-0 active:!shadow-[0_8px_24px_-8px_rgba(139,92,246,0.5)] transition-all"
                  loading={codeLoggingIn}
                  onClick={onEmailCodeLogin}
                >
                  登录
                </Button>
              </Form>
            </TabPane>
          </Tabs>

          <div className="flex items-center justify-center gap-2 mb-1 text-sm">
            <Link to="/register" className="text-[var(--color-text-2)] hover:text-purple-600 no-underline">
              注册账号
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}

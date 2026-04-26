import { useState, useEffect } from 'react'
import { Button, Form, Input, Message, Typography } from '@arco-design/web-react'
import { Link, useNavigate } from 'react-router-dom'
import { registerAccount, sendEmailLoginCode } from '@/api/auth'
import { persistAuthSession } from '@/stores/authStore'

const { Title, Text } = Typography

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function RegisterPage() {
  const navigate = useNavigate()
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)
  const [codeSending, setCodeSending] = useState(false)
  const [countdown, setCountdown] = useState(0)

  const onSendCode = async () => {
    const v = await form.validate(['email']).catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    if (!EMAIL_RE.test(email)) {
      Message.error('请输入有效邮箱')
      return
    }
    setCodeSending(true)
    try {
      await sendEmailLoginCode(email)
      Message.success('验证码已发送，请查收邮箱')
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

  const onSubmit = async () => {
    const v = await form.validate().catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    const password = String(v.password ?? '')
    const password2 = String(v.password2 ?? '')
    const code = String(v.code ?? '').trim()
    if (!EMAIL_RE.test(email)) {
      Message.error('请输入有效邮箱')
      return
    }
    if (code.replace(/\D/g, '').length < 6) {
      Message.error('请输入邮件中的 6 位验证码')
      return
    }
    if (password.length < 6) {
      Message.error('密码至少 6 位')
      return
    }
    if (password !== password2) {
      Message.error('两次输入的密码不一致')
      return
    }
    setSubmitting(true)
    try {
      const session = await registerAccount({
        email,
        password,
        code,
        displayName: String(v.displayName ?? '').trim() || undefined,
      })
      persistAuthSession(session)
      Message.success('注册成功')
      navigate('/', { replace: true })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '注册失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="relative flex flex-col h-screen h-[100dvh] max-h-screen max-h-[100dvh] overflow-hidden">
      <div className="absolute inset-0 bg-[url('/background.png')] bg-center bg-cover" aria-hidden />
      <div className="absolute inset-0 bg-gradient-to-br from-purple-500/15 via-pink-400/10 to-blue-500/15 backdrop-blur-sm" />

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
            注册 LingVoice
          </Title>
        </div>

        <div className="w-full max-w-[420px] rounded-3xl border border-white/40 bg-white/95 p-8 pb-6 shadow-[0_1px_0_rgba(255,255,255,0.6)_inset,0_32px_80px_-24px_rgba(0,0,0,0.2),0_12px_32px_-12px_rgba(0,0,0,0.1)] backdrop-blur-xl animate-[fadeIn_0.5s_ease-out]">
          <Form
            form={form}
            layout="vertical"
            requiredSymbol={false}
            className="mb-5 last:mb-4"
            onSubmit={onSubmit}
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
            <Form.Item field="displayName" label="显示名[可选]">
              <Input
                size="large"
                placeholder="应用显示名"
                autoComplete="nickname"
                className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
              />
            </Form.Item>
            <Form.Item
              field="password"
              label="密码"
              rules={[{ required: true, message: '请设置密码' }]}
            >
              <Input.Password
                size="large"
                placeholder="至少 6 位"
                autoComplete="new-password"
                className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
              />
            </Form.Item>
            <Form.Item
              field="password2"
              label="确认密码"
              rules={[{ required: true, message: '请再次输入密码' }]}
            >
              <Input.Password
                size="large"
                placeholder="再次输入密码"
                autoComplete="new-password"
                className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
              />
            </Form.Item>

            <Button
              type="primary"
              htmlType="submit"
              long
              size="large"
              className="!h-12 !mb-4 !rounded-xl !text-base !font-semibold !border-none !bg-gradient-to-r !from-purple-500 !via-purple-600 !to-purple-700 !shadow-[0_12px_32px_-12px_rgba(139,92,246,0.5)] hover:!-translate-y-0.5 hover:!shadow-[0_16px_40px_-12px_rgba(139,92,246,0.6)] active:!translate-y-0 active:!shadow-[0_8px_24px_-8px_rgba(139,92,246,0.5)] transition-all"
              loading={submitting}
            >
              注册并登录
            </Button>
          </Form>

          <div className="flex items-center justify-center gap-2 mb-1 text-sm">
            <Text type="secondary">已有账号？</Text>{' '}
            <Link to="/login" className="text-[var(--color-text-2)] hover:text-purple-600 no-underline">
              去登录
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}

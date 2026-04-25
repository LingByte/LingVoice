import { useState } from 'react'
import { Button, Form, Input, Message, Typography } from '@arco-design/web-react'
import { Link, useNavigate } from 'react-router-dom'
import { registerAccount } from '@/api/auth'
import { persistAuthSession } from '@/stores/authStore'

const { Title, Text } = Typography

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function RegisterPage() {
  const navigate = useNavigate()
  const [form] = Form.useForm()
  const [submitting, setSubmitting] = useState(false)

  const onSubmit = async () => {
    const v = await form.validate().catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    const password = String(v.password ?? '')
    const password2 = String(v.password2 ?? '')
    if (!EMAIL_RE.test(email)) {
      Message.error('请输入有效邮箱')
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
    <div className="login-page">
      <div className="login-page__bg" aria-hidden />

      <div className="login-page__center">
        <div className="login-page__card">
          <div className="login-page__brand">
            <img
              src="/logo.png"
              alt=""
              width={40}
              height={40}
              className="login-page__brand-logo"
            />
            <Title heading={5} className="!m-0 !text-[var(--color-text-1)]">
              注册 LingVoice
            </Title>
          </div>

          <Form
            form={form}
            layout="vertical"
            requiredSymbol={false}
            className="login-page__form"
            onSubmit={onSubmit}
          >
            <Form.Item
              field="email"
              label="邮箱"
              rules={[{ required: true, message: '请输入邮箱' }]}
            >
              <Input size="large" placeholder="name@example.com" autoComplete="email" />
            </Form.Item>
            <Form.Item field="displayName" label="显示名（可选）">
              <Input size="large" placeholder="如何在应用内展示" autoComplete="nickname" />
            </Form.Item>
            <Form.Item
              field="password"
              label="密码"
              rules={[{ required: true, message: '请设置密码' }]}
            >
              <Input.Password size="large" placeholder="至少 6 位" autoComplete="new-password" />
            </Form.Item>
            <Form.Item
              field="password2"
              label="确认密码"
              rules={[{ required: true, message: '请再次输入密码' }]}
            >
              <Input.Password size="large" placeholder="再次输入密码" autoComplete="new-password" />
            </Form.Item>

            <Text type="secondary" className="!mb-3 !block text-[12px] leading-relaxed">
              注册即表示您同意服务条款与隐私政策。
            </Text>

            <Button
              type="primary"
              htmlType="submit"
              long
              size="large"
              className="login-page__submit"
              loading={submitting}
            >
              注册并登录
            </Button>
          </Form>

          <div className="login-page__links login-page__links--mt">
            <Text type="secondary">已有账号？</Text>{' '}
            <Link to="/login" className="login-page__muted-link !no-underline">
              去登录
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}

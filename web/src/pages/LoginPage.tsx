import { useState } from 'react'
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
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '发送失败')
    } finally {
      setCodeSending(false)
    }
  }

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
    <div className="login-page">
      <div className="login-page__bg" aria-hidden />

      <div className="login-page__toolbar">
        <Select
          size="small"
          defaultValue="zh-CN"
          options={LANG_OPTIONS}
          className="login-page__lang"
        />
      </div>

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
              LingVoice
            </Title>
          </div>

          <Tabs
            activeTab={activeTab}
            onChange={setActiveTab}
            className="login-page__tabs"
          >
            <TabPane key="password" title="账号密码">
              <Form
                form={pwdForm}
                layout="vertical"
                requiredSymbol={false}
                className="login-page__form"
                onSubmit={onPasswordSubmit}
              >
                <Form.Item
                  field="email"
                  label="邮箱"
                  rules={[{ required: true, message: '请输入邮箱' }]}
                >
                  <Input
                    size="large"
                    placeholder="name@example.com"
                    autoComplete="email"
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
                  />
                </Form.Item>

                <Text className="login-page__terms">
                  使用即代表您已阅读并同意{' '}
                  <button type="button" className="login-page__terms-link">
                    服务协议
                  </button>
                  {' '}
                  和{' '}
                  <button type="button" className="login-page__terms-link">
                    隐私协议
                  </button>
                </Text>

                <Button
                  type="primary"
                  htmlType="submit"
                  long
                  size="large"
                  className="login-page__submit"
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
                className="login-page__form"
              >
                <Text type="secondary" className="!mb-3 !block text-[13px] leading-relaxed">
                  输入已注册邮箱，点击获取验证码；查收邮件后填写 6 位数字验证码即可登录（无需密码）。
                </Text>
                <Form.Item
                  field="email"
                  label="邮箱"
                  rules={[{ required: true, message: '请输入邮箱' }]}
                >
                  <Input
                    size="large"
                    placeholder="name@example.com"
                    autoComplete="email"
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
                  />
                </Form.Item>
                <Button
                  type="outline"
                  long
                  size="large"
                  className="login-page__submit !mb-3"
                  loading={codeSending}
                  onClick={onSendCode}
                >
                  获取验证码
                </Button>
                <Button
                  type="primary"
                  long
                  size="large"
                  className="login-page__submit"
                  loading={codeLoggingIn}
                  onClick={onEmailCodeLogin}
                >
                  登录
                </Button>
              </Form>
            </TabPane>
          </Tabs>

          <div className="login-page__links login-page__links--mt">
            <Link to="/register" className="login-page__muted-link !no-underline">
              注册账号
            </Link>
          </div>

          <div className="login-page__foot">
            <Link to="/" className="login-page__foot-link">
              返回首页
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}

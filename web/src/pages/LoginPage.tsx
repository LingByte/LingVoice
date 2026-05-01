import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
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
import { useLocaleStore } from '@/locale/store'
import type { AppLocale } from '@/locale/storage'
import {
  loginWithEmailCode,
  loginWithPassword,
  sendEmailLoginCode,
} from '@/api/auth'
import { persistAuthSession } from '@/stores/authStore'

const { Title } = Typography
const TabPane = Tabs.TabPane

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function LoginPage() {
  const { t } = useTranslation()
  const uiLocale = useLocaleStore((s) => s.locale)
  const setUiLocale = useLocaleStore((s) => s.setLocale)
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
      Message.error(t('auth.invalidEmail'))
      return
    }
    try {
      const session = await loginWithPassword(email, password)
      persistAuthSession(session)
      Message.success(t('auth.loginOk'))
      navigate('/', { replace: true })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : t('auth.loginFail'))
    }
  }

  const onSendCode = async () => {
    const v = await emailForm.validate(['email']).catch(() => null)
    if (!v) return
    const email = String(v.email ?? '').trim()
    if (!EMAIL_RE.test(email)) {
      Message.error(t('auth.invalidEmail'))
      return
    }
    setCodeSending(true)
    try {
      await sendEmailLoginCode(email)
      Message.success(t('auth.codeSentHint'))
      setCountdown(60)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : t('auth.sendFail'))
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
      Message.error(t('auth.invalidEmail'))
      return
    }
    if (code.replace(/\D/g, '').length < 6) {
      Message.error(t('auth.codeInvalid'))
      return
    }
    setCodeLoggingIn(true)
    try {
      const session = await loginWithEmailCode(email, code)
      persistAuthSession(session)
      Message.success(t('auth.loginOk'))
      navigate('/', { replace: true })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : t('auth.codeWrong'))
    } finally {
      setCodeLoggingIn(false)
    }
  }

  return (
    <div className="relative flex flex-col h-screen h-[100dvh] max-h-screen max-h-[100dvh] overflow-hidden">
      <div className="absolute inset-0 bg-[url('/background.png')] bg-center bg-cover" aria-hidden />
      <div className="absolute inset-0 bg-gradient-to-br from-purple-500/15 via-pink-400/10 to-blue-500/15 backdrop-blur-sm" />

      <div className="absolute right-6 top-6 z-10">
        <Select<AppLocale>
          size="small"
          value={uiLocale}
          onChange={(v) => setUiLocale(v as AppLocale)}
          options={[
            { label: t('locale.zhCN'), value: 'zh-CN' },
            { label: t('locale.en'), value: 'en' },
            { label: t('locale.ja'), value: 'ja' },
          ]}
          className="w-36"
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
            {t('auth.brandTitle')}
          </Title>
        </div>

        <div className="w-full max-w-[420px] rounded-3xl border border-white/40 bg-white/95 p-8 pb-6 shadow-[0_1px_0_rgba(255,255,255,0.6)_inset,0_32px_80px_-24px_rgba(0,0,0,0.2),0_12px_32px_-12px_rgba(0,0,0,0.1)] backdrop-blur-xl animate-[fadeIn_0.5s_ease-out]">
          <Tabs
            activeTab={activeTab}
            onChange={setActiveTab}
            className="mb-2"
          >
            <TabPane key="password" title={t('auth.tabPassword')}>
              <Form
                form={pwdForm}
                layout="vertical"
                requiredSymbol={false}
                className="mb-5 last:mb-4"
                onSubmit={onPasswordSubmit}
              >
                <Form.Item
                  field="email"
                  label={t('auth.email')}
                  rules={[{ required: true, message: t('auth.pwdEmailRequired') }]}
                >
                  <Input
                    size="large"
                    placeholder={t('auth.emailPlaceholder')}
                    autoComplete="email"
                    className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
                  />
                </Form.Item>
                <Form.Item
                  field="password"
                  label={t('auth.password')}
                  rules={[{ required: true, message: t('auth.pwdRequired') }]}
                >
                  <Input.Password
                    size="large"
                    placeholder={t('auth.passwordPlaceholder')}
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
                  {t('auth.login')}
                </Button>
              </Form>
            </TabPane>

            <TabPane key="email" title={t('auth.tabEmail')}>
              <Form
                form={emailForm}
                layout="vertical"
                requiredSymbol={false}
                className="mb-5 last:mb-4"
              >
                <Form.Item
                  field="email"
                  label={t('auth.email')}
                  rules={[{ required: true, message: t('auth.pwdEmailRequired') }]}
                >
                  <Input
                    size="large"
                    placeholder={t('auth.emailPlaceholder')}
                    autoComplete="email"
                    className="!rounded-xl !bg-gray-100/80 !border-transparent hover:!bg-gray-100 focus:!bg-white focus:!border-purple-500 focus:!shadow-[0_0_0_4px_rgba(139,92,246,0.15)] transition-all"
                  />
                </Form.Item>
                <Form.Item
                  field="code"
                  label={t('auth.code')}
                  rules={[{ required: true, message: t('auth.codeRequired') }]}
                >
                  <Input
                    size="large"
                    placeholder={t('auth.codePlaceholder')}
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
                        {countdown > 0 ? `${countdown}s` : t('auth.sendCode')}
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
                  {t('auth.login')}
                </Button>
              </Form>
            </TabPane>
          </Tabs>

          <div className="flex items-center justify-center gap-2 mb-1 text-sm">
            <Link to="/register" className="text-[var(--color-text-2)] hover:text-purple-600 no-underline">
              {t('auth.registerLink')}
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}

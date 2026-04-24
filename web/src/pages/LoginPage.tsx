import {
  Button,
  Divider,
  Form,
  Input,
  Select,
  Typography,
} from '@arco-design/web-react'
import { Link, useNavigate } from 'react-router-dom'

const { Title, Text } = Typography

const LANG_OPTIONS = [
  { label: '简体中文', value: 'zh-CN' },
  { label: 'English', value: 'en-US' },
]

export function LoginPage() {
  const navigate = useNavigate()
  const [form] = Form.useForm()

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

          <Form
            form={form}
            layout="vertical"
            requiredSymbol={false}
            className="login-page__form"
            onSubmit={() => {
              navigate('/')
            }}
          >
            <Form.Item field="account" rules={[{ required: true, message: '请输入账号' }]}>
              <Input
                size="large"
                placeholder="用户名 / 手机号"
                autoComplete="username"
              />
            </Form.Item>
            <Form.Item field="password" rules={[{ required: true, message: '请输入密码' }]}>
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
              {' '}和{' '}
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

          <div className="login-page__links">
            <button type="button" className="login-page__muted-link">
              忘记密码？
            </button>
            <span className="login-page__links-sep">|</span>
            <button type="button" className="login-page__muted-link">
              注册账号
            </button>
          </div>

          <Divider className="login-page__divider">或</Divider>

          <Button long size="large" className="login-page__sso">
            企业 SSO 登录
          </Button>

          <div className="login-page__foot">
            <Link to="/" className="login-page__foot-link">
              无法登录？联系支持
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}

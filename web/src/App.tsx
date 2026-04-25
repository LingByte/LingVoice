import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { MainLayout } from '@/layouts/MainLayout'
import { BlankPage } from '@/pages/BlankPage'
import { ChatPage } from '@/pages/ChatPage'
import { LoginPage } from '@/pages/LoginPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { ProfilePage } from '@/pages/ProfilePage'
import { MailLogsPage } from '@/pages/MailLogsPage'
import { MailTemplateEditPage } from '@/pages/MailTemplateEditPage'
import { MailTemplatesPage } from '@/pages/MailTemplatesPage'
import { NotificationChannelEditPage } from '@/pages/NotificationChannelEditPage'
import { NotificationChannelsPage } from '@/pages/NotificationChannelsPage'
import { SettingsPage } from '@/pages/SettingsPage'

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/" element={<MainLayout />}>
          <Route index element={<ChatPage />} />
          <Route path="profile" element={<ProfilePage />} />
          <Route path="credential" element={<BlankPage />} />
          <Route path="quotas" element={<BlankPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="notify/channels" element={<NotificationChannelsPage />} />
          <Route path="notify/channels/:channelId" element={<NotificationChannelEditPage />} />
          <Route path="notify/mail-templates" element={<MailTemplatesPage />} />
          <Route path="notify/mail-templates/:templateId" element={<MailTemplateEditPage />} />
          <Route path="notify/mail-logs" element={<MailLogsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

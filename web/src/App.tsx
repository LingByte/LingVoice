import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { MainLayout } from '@/layouts/MainLayout'
import { BlankPage } from '@/pages/BlankPage'
import { CredentialsPage } from '@/pages/CredentialsPage'
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
import { OpenApiDebugPage } from '@/pages/OpenApiDebugPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { LlmChannelsPage } from '@/pages/LlmChannelsPage'
import { LlmUsagePage } from '@/pages/LlmUsagePage'
import { SpeechUsagePage } from '@/pages/SpeechUsagePage'
import { AsrChannelsPage } from '@/pages/AsrChannelsPage'
import { TtsChannelsPage } from '@/pages/TtsChannelsPage'
import { AgentRunsPage } from '@/pages/AgentRunsPage'
import { AdminUsersPage } from '@/pages/AdminUsersPage'

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/" element={<MainLayout />}>
          <Route index element={<ChatPage />} />
          <Route path="profile" element={<ProfilePage />} />
          <Route path="credential" element={<CredentialsPage />} />
          <Route path="quotas" element={<BlankPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="notify/channels" element={<NotificationChannelsPage />} />
          <Route path="notify/channels/:channelId" element={<NotificationChannelEditPage />} />
          <Route path="notify/mail-templates" element={<MailTemplatesPage />} />
          <Route path="notify/mail-templates/:templateId" element={<MailTemplateEditPage />} />
          <Route path="notify/mail-logs" element={<MailLogsPage />} />
          <Route path="notify/llm-usage" element={<LlmUsagePage />} />
          <Route path="notify/speech-usage" element={<SpeechUsagePage />} />
          <Route path="admin/agent-runs" element={<AgentRunsPage />} />
          <Route path="admin/users" element={<AdminUsersPage />} />
          <Route path="channels/llm" element={<LlmChannelsPage />} />
          <Route path="channels/asr" element={<AsrChannelsPage />} />
          <Route path="channels/tts" element={<TtsChannelsPage />} />
          <Route path="debug/openapi" element={<OpenApiDebugPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

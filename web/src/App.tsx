import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { MainLayout } from '@/layouts/MainLayout'
import { DocsLayout } from '@/layouts/DocsLayout'
import { DashboardPage } from '@/pages/DashboardPage'
import { CredentialsPage } from '@/pages/CredentialsPage'
import { ChatPage } from '@/pages/ChatPage'
import { LoginPage } from '@/pages/LoginPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { ProfilePage } from '@/pages/ProfilePage'
import { MailLogsPage } from '@/pages/MailLogsPage'
import { SmsLogsPage } from '@/pages/SmsLogsPage'
import { MailTemplateEditPage } from '@/pages/MailTemplateEditPage'
import { MailTemplatesPage } from '@/pages/MailTemplatesPage'
import { NotificationChannelEditPage } from '@/pages/NotificationChannelEditPage'
import { NotificationChannelsPage } from '@/pages/NotificationChannelsPage'
import { V1ApiDebugPage } from '@/pages/V1ApiDebugPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { LlmAbilitiesPage } from '@/pages/LlmAbilitiesPage'
import { LlmChannelsPage } from '@/pages/LlmChannelsPage'
import { LlmModelMetasPage } from '@/pages/LlmModelMetasPage'
import { ModelPlazaPage } from '@/pages/ModelPlazaPage'
import { AdminOnly } from '@/components/AdminOnly'
import { LlmUsagePage } from '@/pages/LlmUsagePage'
import { UsageLogsPage } from '@/pages/UsageLogsPage'
import { SpeechUsagePage } from '@/pages/SpeechUsagePage'
import { AsrChannelsPage } from '@/pages/AsrChannelsPage'
import { TtsChannelsPage } from '@/pages/TtsChannelsPage'
import { AgentRunsPage } from '@/pages/AgentRunsPage'
import { AdminUsersPage } from '@/pages/AdminUsersPage'
import { AdminAnnouncementsPage } from '@/pages/AdminAnnouncementsPage'
import { AboutPage } from '@/pages/AboutPage'
import { AnnouncementsPage } from '@/pages/AnnouncementsPage'
import { DocsPage } from '@/pages/DocsPage'

export default function App() {
  return (
    <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/" element={<MainLayout />}>
          <Route index element={<ChatPage />} />
          <Route path="profile" element={<ProfilePage />} />
          <Route path="credential" element={<CredentialsPage />} />
          <Route path="dashboard" element={<DashboardPage />} />
          <Route path="quotas" element={<DashboardPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="notify/channels" element={<NotificationChannelsPage />} />
          <Route path="notify/channels/:channelId" element={<NotificationChannelEditPage />} />
          <Route path="notify/mail-templates" element={<MailTemplatesPage />} />
          <Route path="notify/mail-templates/:templateId" element={<MailTemplateEditPage />} />
          <Route path="notify/mail-logs" element={<MailLogsPage />} />
          <Route path="notify/sms-logs" element={<SmsLogsPage />} />
          <Route
            path="notify/llm-usage"
            element={
              <AdminOnly title="LLM 用量">
                <LlmUsagePage />
              </AdminOnly>
            }
          />
          <Route path="usage/llm-logs" element={<UsageLogsPage />} />
          <Route
            path="notify/speech-usage"
            element={
              <AdminOnly title="语音用量">
                <SpeechUsagePage />
              </AdminOnly>
            }
          />
          <Route path="admin/agent-runs" element={<AgentRunsPage />} />
          <Route path="admin/users" element={<AdminUsersPage />} />
          <Route path="admin/announcements" element={<AdminAnnouncementsPage />} />
          <Route path="about" element={<AboutPage />} />
          <Route path="announcements" element={<AnnouncementsPage />} />
          <Route path="channels/llm" element={<LlmChannelsPage />} />
          <Route path="channels/llm-abilities" element={<LlmAbilitiesPage />} />
          <Route path="channels/llm-model-metas" element={<LlmModelMetasPage />} />
          <Route path="channels/llm-plaza" element={<ModelPlazaPage />} />
          <Route path="channels/asr" element={<AsrChannelsPage />} />
          <Route path="channels/tts" element={<TtsChannelsPage />} />
          <Route path="debug/v1" element={<V1ApiDebugPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
        <Route path="/docs" element={<DocsLayout />}>
          <Route index element={<DocsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

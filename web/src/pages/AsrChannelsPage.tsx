import { SpeechChannelsListPage } from '@/pages/SpeechChannelsListPage'

export function AsrChannelsPage() {
  return (
    <SpeechChannelsListPage
      kind="asr"
      title="ASR 语音识别渠道"
      description="按厂商在 configJson 中存放鉴权、endpoint、模型等；与 LLM 渠道分表。"
    />
  )
}

import { SpeechChannelsListPage } from '@/pages/SpeechChannelsListPage'

export function TtsChannelsPage() {
  return (
    <SpeechChannelsListPage
      kind="tts"
      title="TTS 语音合成渠道"
      description="按厂商在 configJson 中存放音色、鉴权、endpoint 等；与 LLM 渠道分表。"
    />
  )
}

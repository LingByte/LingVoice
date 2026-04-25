/**
 * ASR/TTS 控制台厂商元数据（仅前端）；与后端 recognizer config_builder、synthesizer NewSynthesisServiceFromCredential 的 provider 字符串对齐。
 */

export type SpeechConfigField = {
  key: string
  label: string
  type: 'string' | 'password' | 'number' | 'textarea'
  required: boolean
  placeholder?: string
  hint?: string
}

export type SpeechProviderMeta = {
  id: string
  label: string
  description?: string
  notes?: string
  configFields: SpeechConfigField[]
}

export function normalizeASRProviderId(p: string): string {
  const x = p.toLowerCase().trim()
  switch (x) {
    case 'tencent':
      return 'qcloud'
    case 'aliyun_funasr':
    case 'aliyun-funasr':
    case 'aliyun':
      return 'funasr'
    case 'volcengine_llm':
      return 'volcllmasr'
    default:
      return p.trim()
  }
}

export function normalizeTTSProviderId(p: string): string {
  const x = p.toLowerCase().trim()
  if (x === 'tencent') return 'qcloud'
  return p.trim()
}

export const ASR_PROVIDER_LIST: SpeechProviderMeta[] = [
  {
    id: 'qcloud',
    label: '腾讯云 ASR',
    description: '与 recognizer NewTranscriberConfigFromMap 的 qcloud/tencent 分支一致。',
    configFields: [
      { key: 'appId', label: 'AppId', type: 'string', required: true, placeholder: '应用 ID' },
      { key: 'secretId', label: 'SecretId', type: 'string', required: true },
      { key: 'secretKey', label: 'SecretKey', type: 'password', required: true, hint: '也可用 JSON 键 secret' },
      { key: 'modelType', label: 'ModelType', type: 'string', required: false, placeholder: '默认 16k_zh' },
    ],
  },
  {
    id: 'google',
    label: 'Google Cloud Speech',
    description: '流式识别依赖运行环境；此处仅存采样与语言等参数。',
    configFields: [
      { key: 'languageCode', label: 'LanguageCode', type: 'string', required: false, placeholder: 'zh-CN' },
      { key: 'sampleRate', label: 'SampleRate (Hz)', type: 'number', required: false, placeholder: '16000' },
      { key: 'encoding', label: 'Encoding', type: 'string', required: false, placeholder: 'LINEAR16' },
    ],
  },
  {
    id: 'funasr',
    label: 'FunASR（阿里云 DashScope 等）',
    configFields: [
      { key: 'url', label: 'WebSocket URL', type: 'string', required: false, placeholder: 'wss://dashscope.aliyuncs.com/...' },
      { key: 'mode', label: 'Mode', type: 'string', required: false },
      { key: 'audioFs', label: 'AudioFs', type: 'number', required: false, placeholder: '16000' },
    ],
  },
  {
    id: 'funasr_realtime',
    label: 'FunASR 实时',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'url', label: 'WebSocket URL', type: 'string', required: false },
      { key: 'model', label: 'Model', type: 'string', required: false, placeholder: 'fun-asr-realtime' },
      { key: 'sampleRate', label: 'SampleRate', type: 'number', required: false, placeholder: '16000' },
    ],
  },
  {
    id: 'volcengine',
    label: '火山引擎标准 ASR',
    notes: '部分场景需配合 media pipeline；凭据字段需完整。',
    configFields: [
      { key: 'appId', label: 'AppId', type: 'string', required: true },
      { key: 'token', label: 'Token', type: 'password', required: true },
      { key: 'cluster', label: 'Cluster', type: 'string', required: false, placeholder: 'volcano_tts' },
      { key: 'url', label: 'WebSocket URL', type: 'string', required: false },
    ],
  },
  {
    id: 'volcllmasr',
    label: '火山 LLM ASR',
    description: '与 volcengine_llm 同义，保存为 volcllmasr。',
    configFields: [
      { key: 'appId', label: 'AppId', type: 'string', required: true },
      { key: 'token', label: 'Token', type: 'password', required: true },
    ],
  },
  {
    id: 'gladia',
    label: 'Gladia',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'encoding', label: 'Encoding', type: 'string', required: false, placeholder: 'WAV/PCM' },
    ],
  },
  {
    id: 'deepgram',
    label: 'Deepgram',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'model', label: 'Model', type: 'string', required: false, placeholder: 'nova-2' },
      { key: 'language', label: 'Language', type: 'string', required: false, placeholder: 'en-US' },
    ],
  },
  {
    id: 'aws',
    label: 'AWS Transcribe',
    configFields: [
      { key: 'appId', label: 'AccessKeyId（键名 appId）', type: 'string', required: false },
      { key: 'region', label: 'Region', type: 'string', required: false, placeholder: 'us-east-1' },
      { key: 'language', label: 'Language', type: 'string', required: false },
    ],
  },
  {
    id: 'baidu',
    label: '百度 ASR',
    configFields: [
      { key: 'appId', label: 'AppId（数字）', type: 'string', required: true },
      { key: 'appKey', label: 'AppKey', type: 'password', required: true },
      { key: 'devPid', label: 'DevPid', type: 'number', required: false, placeholder: '1537' },
    ],
  },
  {
    id: 'voiceapi',
    label: 'VoiceAPI',
    configFields: [{ key: 'url', label: '服务 URL', type: 'string', required: true }],
  },
  {
    id: 'whisper',
    label: 'Whisper（OpenAI 兼容）',
    configFields: [
      { key: 'url', label: 'Base URL', type: 'string', required: true, placeholder: 'https://api.openai.com/v1' },
      { key: 'model', label: 'Model', type: 'string', required: false, placeholder: 'whisper-1' },
    ],
  },
  {
    id: 'local',
    label: '本地 ASR',
    configFields: [{ key: 'modelPath', label: 'ModelPath', type: 'string', required: false }],
  },
]

export const TTS_PROVIDER_LIST: SpeechProviderMeta[] = [
  {
    id: 'qcloud',
    label: '腾讯云 TTS',
    configFields: [
      { key: 'appId', label: 'AppId', type: 'string', required: true },
      { key: 'secretId', label: 'SecretId', type: 'string', required: true },
      { key: 'secretKey', label: 'SecretKey', type: 'password', required: true },
      { key: 'voiceType', label: 'VoiceType', type: 'string', required: false, placeholder: '如 601002' },
      { key: 'codec', label: 'Codec', type: 'string', required: false, placeholder: 'pcm' },
      { key: 'sampleRate', label: 'SampleRate', type: 'number', required: false, placeholder: '16000' },
    ],
  },
  {
    id: 'qiniu',
    label: '七牛 TTS',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'baseUrl', label: 'BaseUrl', type: 'string', required: false },
    ],
  },
  {
    id: 'baidu',
    label: '百度 TTS',
    configFields: [
      { key: 'token', label: 'Token', type: 'password', required: true },
      { key: 'language', label: 'Language', type: 'string', required: false },
    ],
  },
  {
    id: 'azure',
    label: 'Azure Speech',
    configFields: [
      { key: 'subscriptionKey', label: 'SubscriptionKey', type: 'password', required: true },
      { key: 'region', label: 'Region', type: 'string', required: true, placeholder: 'eastasia' },
      { key: 'voice', label: '默认 Voice', type: 'string', required: false },
      { key: 'language', label: 'Language', type: 'string', required: false },
    ],
  },
  {
    id: 'xunfei',
    label: '讯飞 TTS',
    configFields: [
      { key: 'appId', label: 'AppId', type: 'string', required: true },
      { key: 'apiKey', label: 'ApiKey', type: 'string', required: true },
      { key: 'apiSecret', label: 'ApiSecret', type: 'password', required: true },
    ],
  },
  {
    id: 'openai',
    label: 'OpenAI TTS',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'baseUrl', label: 'BaseUrl', type: 'string', required: false, placeholder: 'https://api.openai.com' },
    ],
  },
  {
    id: 'google',
    label: 'Google Cloud TTS',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'projectId', label: 'ProjectId', type: 'string', required: false },
      { key: 'languageCode', label: 'LanguageCode', type: 'string', required: false, placeholder: 'en-US' },
    ],
  },
  {
    id: 'aws',
    label: 'Amazon Polly',
    configFields: [
      { key: 'accessKeyId', label: 'AccessKeyId', type: 'string', required: true },
      { key: 'secretAccessKey', label: 'SecretAccessKey', type: 'password', required: true },
      { key: 'region', label: 'Region', type: 'string', required: false, placeholder: 'us-east-1' },
    ],
  },
  {
    id: 'volcengine',
    label: '火山引擎 TTS',
    configFields: [
      { key: 'appId', label: 'AppId', type: 'string', required: true },
      { key: 'accessToken', label: 'AccessToken', type: 'password', required: true },
      { key: 'cluster', label: 'Cluster', type: 'string', required: false },
      { key: 'voiceType', label: 'VoiceType', type: 'string', required: false },
    ],
  },
  {
    id: 'minimax',
    label: 'Minimax',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'model', label: 'Model', type: 'string', required: false },
      { key: 'voiceId', label: 'VoiceId', type: 'string', required: false },
    ],
  },
  {
    id: 'elevenlabs',
    label: 'ElevenLabs',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'voiceId', label: 'VoiceId', type: 'string', required: false },
      { key: 'modelId', label: 'ModelId', type: 'string', required: false },
    ],
  },
  {
    id: 'local',
    label: '本地 TTS（命令行）',
    configFields: [
      { key: 'command', label: 'Command', type: 'string', required: false, placeholder: 'say' },
      { key: 'voice', label: 'Voice', type: 'string', required: false },
    ],
  },
  {
    id: 'fishspeech',
    label: 'FishSpeech',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'referenceId', label: 'ReferenceId', type: 'string', required: false },
    ],
  },
  {
    id: 'fishaudio',
    label: 'Fish Audio',
    configFields: [
      { key: 'apiKey', label: 'ApiKey', type: 'password', required: true },
      { key: 'referenceId', label: 'ReferenceId', type: 'string', required: false },
    ],
  },
  {
    id: 'coqui',
    label: 'Coqui TTS',
    configFields: [
      { key: 'url', label: '服务 URL', type: 'string', required: true },
      { key: 'language', label: 'Language', type: 'string', required: false },
      { key: 'speaker', label: 'Speaker', type: 'string', required: false },
    ],
  },
  {
    id: 'local_gospeech',
    label: '本地 go-speech',
    configFields: [
      { key: 'modelPath', label: 'ModelPath', type: 'string', required: true },
      { key: 'language', label: 'Language', type: 'string', required: false },
      { key: 'speaker', label: 'Speaker', type: 'string', required: false },
    ],
  },
]

export function speechProvidersForKind(kind: 'asr' | 'tts'): SpeechProviderMeta[] {
  return kind === 'asr' ? ASR_PROVIDER_LIST : TTS_PROVIDER_LIST
}

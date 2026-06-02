# 00 · 愿景与边界

## 我们要做什么

一套 **以 AI 编排为底层** 的平台能力，最终能支撑语音联络场景（外呼、客服、Agent），但 **第一步只做 LLM 编排基座**，不碰电话、不碰 UI。

可以把它理解成：

> 先有自己的「LLM 运行时 + 最小编排内核」，语音、SIP、Campaign 都是以后挂在这个内核上的扩展层。

## 不做什么（当前阶段）

- 不 vendoring / fork 四个参考项目进本仓库
- 不做 LiveKit Room、不做嵌入式 SIP
- 不做多租户、外呼任务、运营台
- 不做可视化编排编辑器

## 参考项目各自学什么

| 项目 | 借鉴点 | 本阶段是否实现 |
|------|--------|----------------|
| Eino | Graph、Runnable、Stream 合并、Tool 节点 | 仅概念；Phase 0 不做 Graph |
| LiveKit Agents | AgentSession、Job、Provider 插件形态 | 暂不实现 |
| LingEchoX | MediaPort 解耦、Pipeline Stage | 语音层再议 |
| LiveKit SIP | Gateway 化、Dispatch | 媒体层再议 |

## 成功标准（Phase 0 结束）

1. 能用统一接口调用至少一种 LLM（OpenAI 兼容即可）。
2. 能用 `Run` 记录一次完整调用的输入、输出、耗时、错误。
3. 能用最简单的 Chain 串两步（例如：模板填充 → ChatModel）。
4. 文档与 `pkg/` 接口一致，后续加 Tool 不需要改 Message 定义。

## 命名空间

- Go module：`github.com/LingByte/LingVoice`
- 编排相关包前缀：`pkg/llm`、`pkg/orchestrate`、`pkg/protocol`

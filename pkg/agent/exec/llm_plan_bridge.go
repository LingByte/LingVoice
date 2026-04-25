// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package exec

import (
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/agent/plan"
	"github.com/LingByte/LingVoice/pkg/llm"
)

// PlanLLMBridge 将 llm.LLMHandler 适配为 plan.LLM，并在每次 Query 时带上 QueryOptions（用量审计、渠道等）。
type PlanLLMBridge struct {
	H    llm.LLMHandler
	Base *llm.QueryOptions
	RT   string
}

// NewPlanLLMBridge 构建 plan.LLM 实现；requestType 为空时使用 openapi_agent。
func NewPlanLLMBridge(h llm.LLMHandler, base *llm.QueryOptions, requestType string) plan.LLM {
	return PlanLLMBridge{H: h, Base: base, RT: requestType}
}

func (p PlanLLMBridge) Query(text, model string) (string, error) {
	if p.H == nil {
		return "", fmt.Errorf("nil llm handler")
	}
	opts := llm.QueryOptions{}
	if p.Base != nil {
		opts = *p.Base
	}
	opts.Model = model
	rt := strings.TrimSpace(p.RT)
	if rt == "" {
		rt = "openapi_agent"
	}
	opts.RequestType = rt
	resp, err := p.H.QueryWithOptions(text, &opts)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty llm response")
	}
	return strings.TrimSpace(resp.Choices[0].Content), nil
}

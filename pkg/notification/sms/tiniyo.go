package sms

import (
	"context"
	"fmt"
	"strings"
)

type TiniyoConfig struct {
	AccountSID string
	Token      string
	From       string
}

type TiniyoProvider struct {
	cfg TiniyoConfig
}

func NewTiniyo(cfg TiniyoConfig) (*TiniyoProvider, error) {
	if strings.TrimSpace(cfg.AccountSID) == "" || strings.TrimSpace(cfg.Token) == "" || strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("%w: tiniyo requires accountSid/token/from", ErrInvalidConfig)
	}
	return &TiniyoProvider{cfg: cfg}, nil
}

func (p *TiniyoProvider) Kind() ProviderKind { return ProviderTiniyo }

func (p *TiniyoProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	_ = ctx
	if err := ValidateBasic(req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Message.Content) == "" {
		return nil, fmt.Errorf("%w: tiniyo requires content", ErrInvalidArgument)
	}
	return &SendResult{Provider: p.Kind(), Accepted: false, Error: ErrNotImplemented.Error(), SentAtUnix: nowUnix()}, ErrNotImplemented
}


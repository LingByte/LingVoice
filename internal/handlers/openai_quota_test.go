// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"testing"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestQuotaDeltaOpenAI_NewAPIDocStyleExamples(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:quota_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.LLMModelMeta{}); err != nil {
		t.Fatal(err)
	}
	now := int64(1)
	row1 := models.LLMModelMeta{
		ModelName:            "gpt-doc-ex1",
		Status:               1,
		QuotaBillingMode:     "token",
		QuotaModelRatio:      15,
		QuotaPromptRatio:     1,
		QuotaCompletionRatio: 2,
		QuotaCacheReadRatio:  0.25,
		CreatedTime:          now,
		UpdatedTime:          now,
	}
	if err := db.Create(&row1).Error; err != nil {
		t.Fatal(err)
	}
	// (1000 + Round(500*2)) * 15 * 1 = 30000 — new-api 文档示例
	u1 := OpenAIUsageNumbers{Model: "gpt-doc-ex1", PromptTokens: 1000, CompletionTokens: 500}
	if got := QuotaDeltaOpenAI(db, "gpt-doc-ex1", u1, 1); got != 30000 {
		t.Fatalf("ex1: got %d want 30000", got)
	}

	row2 := models.LLMModelMeta{
		ModelName:            "gpt-doc-ex2",
		Status:               1,
		QuotaBillingMode:     "token",
		QuotaModelRatio:      0.25,
		QuotaPromptRatio:     1,
		QuotaCompletionRatio: 1.33,
		QuotaCacheReadRatio:  0.25,
		CreatedTime:          now,
		UpdatedTime:          now,
	}
	if err := db.Create(&row2).Error; err != nil {
		t.Fatal(err)
	}
	u2 := OpenAIUsageNumbers{Model: "gpt-doc-ex2", PromptTokens: 2000, CompletionTokens: 1000}
	if got := QuotaDeltaOpenAI(db, "gpt-doc-ex2", u2, 0.5); got != 416 {
		t.Fatalf("ex2: got %d want 416", got)
	}
}

func TestQuotaDeltaOpenAI_NoUsageReturnsZero(t *testing.T) {
	u := OpenAIUsageNumbers{Model: "any", PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}
	if got := QuotaDeltaOpenAI(nil, "any", u, 1); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
}

func TestQuotaDeltaOpenAI_RoundSmallestNonZeroCharge(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:quota_test2?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.LLMModelMeta{}); err != nil {
		t.Fatal(err)
	}
	now := int64(1)
	row := models.LLMModelMeta{
		ModelName:            "tiny-charge",
		Status:               1,
		QuotaBillingMode:     "token",
		QuotaModelRatio:      0.0001,
		QuotaPromptRatio:     1,
		QuotaCompletionRatio: 1,
		CreatedTime:          now,
		UpdatedTime:          now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	u := OpenAIUsageNumbers{Model: "tiny-charge", PromptTokens: 1, CompletionTokens: 0}
	if got := QuotaDeltaOpenAI(db, "tiny-charge", u, 1); got != 1 {
		t.Fatalf("rounded to 0 but ratio non-zero: got %d want 1", got)
	}
}

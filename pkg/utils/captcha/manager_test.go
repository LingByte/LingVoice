package captcha

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

import (
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	config := DefaultConfig()
	manager := NewManager(config)
	if manager == nil {
		t.Fatal("NewManager returned nil")
	}
	if manager.imageCaptcha == nil {
		t.Fatal("ImageCaptcha is nil")
	}
}

func TestManager_GenerateImage(t *testing.T) {
	manager := NewManager(DefaultConfig())
	result, err := manager.GenerateImage()
	if err != nil {
		t.Fatalf("GenerateImage failed: %v", err)
	}
	if result == nil {
		t.Fatal("GenerateImage returned nil")
	}
	if result.Type != TypeImage {
		t.Fatalf("Expected type %s, got %s", TypeImage, result.Type)
	}
}

func TestManager_VerifyImage(t *testing.T) {
	manager := NewManager(DefaultConfig())
	result, err := manager.GenerateImage()
	if err != nil {
		t.Fatalf("GenerateImage failed: %v", err)
	}

	code, ok := result.Data["code"].(string)
	if !ok {
		t.Fatal("Code not found in result data")
	}

	valid, err := manager.VerifyImage(result.ID, code)
	if err != nil {
		t.Fatalf("VerifyImage failed: %v", err)
	}
	if !valid {
		t.Fatal("Expected valid verification")
	}
}

func TestInitGlobalManager(t *testing.T) {
	config := DefaultConfig()
	InitGlobalManager(config)
	if GlobalManager == nil {
		t.Fatal("GlobalManager should be initialized")
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if config.ImageWidth != 200 {
		t.Fatalf("Expected ImageWidth 200, got %d", config.ImageWidth)
	}
	if config.ImageHeight != 60 {
		t.Fatalf("Expected ImageHeight 60, got %d", config.ImageHeight)
	}
	if config.ImageLength != 4 {
		t.Fatalf("Expected ImageLength 4, got %d", config.ImageLength)
	}
	if config.Expiration != 5*time.Minute {
		t.Fatalf("Expected Expiration 5m, got %v", config.Expiration)
	}
}

func TestManager_GenerateClick(t *testing.T) {
	manager := NewManager(DefaultConfig())
	result, err := manager.GenerateClick()
	if err != nil {
		t.Fatalf("GenerateClick failed: %v", err)
	}
	if result == nil {
		t.Fatal("GenerateClick returned nil")
	}
	if result.Type != TypeClick {
		t.Fatalf("Expected type %s, got %s", TypeClick, result.Type)
	}
}

func TestManager_VerifyClick(t *testing.T) {
	manager := NewManager(DefaultConfig())
	result, err := manager.GenerateClick()
	if err != nil {
		t.Fatalf("GenerateClick failed: %v", err)
	}

	positions, ok := result.Data["positions"].([]Point)
	if !ok {
		t.Fatal("Positions not found in result data")
	}

	valid, err := manager.VerifyClick(result.ID, positions)
	if err != nil {
		t.Fatalf("VerifyClick failed: %v", err)
	}
	if !valid {
		t.Fatal("Expected valid verification")
	}
}

func TestManager_VerifyWithClickAndPuzzle(t *testing.T) {
	manager := NewManager(DefaultConfig())

	// 测试点击验证码验证
	clickResult, err := manager.GenerateClick()
	if err != nil {
		t.Fatalf("GenerateClick failed: %v", err)
	}
	positions, ok := clickResult.Data["positions"].([]Point)
	if !ok {
		t.Fatal("Positions not found")
	}
	valid, err := manager.Verify(TypeClick, clickResult.ID, positions)
	if err != nil {
		t.Fatalf("Verify(TypeClick) failed: %v", err)
	}
	if !valid {
		t.Fatal("Expected valid verification for click captcha")
	}
}

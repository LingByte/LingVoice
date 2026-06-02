package utils

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnv retrieves an environment variable with a default value
func GetEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// GetEnvInt retrieves an environment variable as integer with a default value
func GetEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// GetEnvBool retrieves an environment variable as boolean with a default value
func GetEnvBool(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return defaultValue
}

// ComputeSampleByteCount calculates the byte count for audio samples
// sampleCount: number of audio samples
// sampleRate: sample rate in Hz
// channels: number of audio channels
// bitDepth: bits per sample (8, 16, 24, 32)
func ComputeSampleByteCount(sampleCount int, sampleRate int, channels int, bitDepth int) int {
	if sampleCount <= 0 || sampleRate <= 0 || channels <= 0 || bitDepth <= 0 {
		return 0
	}

	// Calculate bytes per sample
	bytesPerSample := bitDepth / 8

	// Calculate total bytes
	totalBytes := sampleCount * channels * bytesPerSample

	return totalBytes
}

// ComputeSampleCount calculates the number of audio samples
// duration: duration in milliseconds
// sampleRate: sample rate in Hz
func ComputeSampleCount(duration int, sampleRate int) int {
	if duration <= 0 || sampleRate <= 0 {
		return 0
	}

	// Calculate samples: (duration in ms) * (sample rate in Hz) / 1000
	samples := (duration * sampleRate) / 1000

	return samples
}

// ComputeAudioDuration calculates the duration of audio data
// byteCount: number of bytes
// sampleRate: sample rate in Hz
// channels: number of audio channels
// bitDepth: bits per sample (8, 16, 24, 32)
// Returns duration in milliseconds
func ComputeAudioDuration(byteCount int, sampleRate int, channels int, bitDepth int) int {
	if byteCount <= 0 || sampleRate <= 0 || channels <= 0 || bitDepth <= 0 {
		return 0
	}

	// Calculate bytes per sample
	bytesPerSample := bitDepth / 8

	// Calculate number of samples
	samples := byteCount / (channels * bytesPerSample)

	// Calculate duration in milliseconds
	duration := (samples * 1000) / sampleRate

	return duration
}

// NormalizeFramePeriod normalizes a frame period duration string to time.Duration
// Accepts formats like "20ms", "0.02s", "20", etc.
// Returns normalized duration, defaults to 20ms if invalid
func NormalizeFramePeriod(d string) time.Duration {
	if d == "" {
		return 20 * time.Millisecond
	}

	// Try to parse as duration string
	parsed, err := time.ParseDuration(d)
	if err == nil && parsed > 0 {
		// Validate range: 10ms to 300ms
		if parsed < 10*time.Millisecond {
			return 20 * time.Millisecond
		}
		if parsed > 300*time.Millisecond {
			return 20 * time.Millisecond
		}
		return parsed
	}

	// Try to parse as milliseconds (numeric string)
	if ms, err := strconv.Atoi(strings.TrimSpace(d)); err == nil && ms > 0 {
		duration := time.Duration(ms) * time.Millisecond
		if duration < 10*time.Millisecond {
			return 20 * time.Millisecond
		}
		if duration > 300*time.Millisecond {
			return 20 * time.Millisecond
		}
		return duration
	}

	// Default to 20ms
	return 20 * time.Millisecond
}

// FramePeriodToMilliseconds converts a frame period to milliseconds
func FramePeriodToMilliseconds(d time.Duration) int {
	return int(d.Milliseconds())
}

// MillisecondsToFramePeriod converts milliseconds to time.Duration
func MillisecondsToFramePeriod(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// CalculateFrameRate calculates frame rate from frame period
// framePeriod: duration of one frame
// Returns frames per second
func CalculateFrameRate(framePeriod time.Duration) float64 {
	if framePeriod <= 0 {
		return 0
	}
	return 1.0 / framePeriod.Seconds()
}

// CalculateFramePeriod calculates frame period from frame rate
// frameRate: frames per second
// Returns duration of one frame
func CalculateFramePeriod(frameRate float64) time.Duration {
	if frameRate <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / frameRate)
}

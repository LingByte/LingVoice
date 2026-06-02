package avflow

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"image"
	"time"
)

// VideoFrame represents a single video frame with metadata
type VideoFrame struct {
	// Raw frame data
	Data []byte

	// Frame dimensions
	Width  int
	Height int

	// Pixel format (e.g., "yuv420p", "rgb24", "bgr24", "nv12")
	PixelFormat string

	// Codec (e.g., "h264", "vp8", "vp9", "av1", "raw")
	Codec string

	// Timestamp in milliseconds
	Timestamp int64

	// Frame number
	FrameNumber int64

	// Whether this is a keyframe
	IsKeyFrame bool

	// Frame duration in milliseconds
	Duration int32
}

// Detection represents a detected object or region
type Detection struct {
	// Bounding box
	Box BoundingBox

	// Detection class/label
	Label string

	// Confidence score (0.0 to 1.0)
	Confidence float32

	// Additional metadata
	Metadata map[string]interface{}
}

// BoundingBox represents a rectangular region
type BoundingBox struct {
	X      int32 // Left coordinate
	Y      int32 // Top coordinate
	Width  int32 // Width
	Height int32 // Height
}

// FaceDetection represents a detected face with landmarks
type FaceDetection struct {
	// Bounding box of the face
	Box BoundingBox

	// Confidence score
	Confidence float32

	// Facial landmarks (e.g., eyes, nose, mouth)
	Landmarks map[string][2]float32

	// Face ID for tracking
	TrackID int32

	// Additional metadata
	Metadata map[string]interface{}
}

// EmotionResult represents emotion detection result
type EmotionResult struct {
	// Primary emotion
	Emotion string

	// Confidence scores for each emotion
	Scores map[string]float32

	// Face ID this emotion belongs to
	FaceID int32

	// Timestamp
	Timestamp time.Time
}

// ObjectDetectionResult represents results from object detection
type ObjectDetectionResult struct {
	// List of detected objects
	Detections []Detection

	// Frame metadata
	FrameNumber int64
	Timestamp   int64

	// Processing time in milliseconds
	ProcessingTime int32
}

// FaceDetectionResult represents results from face detection
type FaceDetectionResult struct {
	// List of detected faces
	Faces []FaceDetection

	// Frame metadata
	FrameNumber int64
	Timestamp   int64

	// Processing time in milliseconds
	ProcessingTime int32
}

// OCRResult represents optical character recognition result
type OCRResult struct {
	// Recognized text
	Text string

	// Text regions with bounding boxes
	TextRegions []TextRegion

	// Confidence score
	Confidence float32

	// Frame metadata
	FrameNumber int64
	Timestamp   int64

	// Processing time in milliseconds
	ProcessingTime int32
}

// TextRegion represents a region of recognized text
type TextRegion struct {
	// Text content
	Text string

	// Bounding box
	Box BoundingBox

	// Confidence score
	Confidence float32
}

// VideoCodec represents supported video codecs
type VideoCodec string

const (
	CodecH264 VideoCodec = "h264"
	CodecVP8  VideoCodec = "vp8"
	CodecVP9  VideoCodec = "vp9"
	CodecAV1  VideoCodec = "av1"
	CodecRaw  VideoCodec = "raw"
)

// PixelFormat represents supported pixel formats
type PixelFormat string

const (
	PixelFormatYUV420P PixelFormat = "yuv420p"
	PixelFormatRGB24   PixelFormat = "rgb24"
	PixelFormatBGR24   PixelFormat = "bgr24"
	PixelFormatNV12    PixelFormat = "nv12"
	PixelFormatRGBA    PixelFormat = "rgba"
)

// VideoConfig represents video configuration
type VideoConfig struct {
	// Frame dimensions
	Width  int
	Height int

	// Frame rate (fps)
	FrameRate int

	// Pixel format
	PixelFormat PixelFormat

	// Codec
	Codec VideoCodec

	// Bitrate in kbps (for encoded video)
	Bitrate int

	// Buffer size for frames
	BufferSize int
}

// DefaultVideoConfig returns default video configuration
func DefaultVideoConfig() VideoConfig {
	return VideoConfig{
		Width:       1280,
		Height:      720,
		FrameRate:   30,
		PixelFormat: PixelFormatYUV420P,
		Codec:       CodecH264,
		Bitrate:     2500,
		BufferSize:  64,
	}
}

// ConvertToImage converts VideoFrame to image.Image for processing
func (vf *VideoFrame) ConvertToImage() (image.Image, error) {
	// This is a placeholder - actual implementation depends on pixel format
	// For now, we'll just return nil and let callers handle format conversion
	return nil, nil
}

// Clone creates a deep copy of the VideoFrame
func (vf *VideoFrame) Clone() *VideoFrame {
	data := make([]byte, len(vf.Data))
	copy(data, vf.Data)

	return &VideoFrame{
		Data:        data,
		Width:       vf.Width,
		Height:      vf.Height,
		PixelFormat: vf.PixelFormat,
		Codec:       vf.Codec,
		Timestamp:   vf.Timestamp,
		FrameNumber: vf.FrameNumber,
		IsKeyFrame:  vf.IsKeyFrame,
		Duration:    vf.Duration,
	}
}

// Metadata for VideoFrame
type VideoFrameMetadata struct {
	// Camera ID
	CameraID string

	// Scene description
	Scene string

	// Lighting condition
	LightingCondition string

	// Motion level (0.0 to 1.0)
	MotionLevel float32

	// Custom metadata
	Custom map[string]interface{}
}

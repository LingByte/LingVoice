---
description: Multimodal Video Components and Integration Guide
---

# Multimodal Video Components Guide

## Overview

This guide covers the video processing components added to the LingVoice framework, enabling sophisticated multimodal audio+video applications.

## Video Components

### 1. VideoCaptureComponent

Captures video frames from a camera or media source.

**Inputs:** None  
**Outputs:** `video_out` (VideoFrame packets)

**Configuration:**
```go
videoConfig := avflow.VideoConfig{
    Width:       1280,
    Height:      720,
    FrameRate:   30,
    PixelFormat: avflow.PixelFormatYUV420P,
    Codec:       avflow.CodecH264,
    Bitrate:     2500,
    BufferSize:  64,
}

source := avflow.NewMockVideoCaptureSource(videoConfig)
component := avflow.NewVideoCaptureComponent("video-capture", source)
```

**Features:**
- Configurable resolution and frame rate
- Multiple pixel formats (YUV420P, RGB24, BGR24, RGBA)
- Multiple codecs (H264, VP8, VP9, AV1, Raw)
- Frame numbering and timestamping
- Keyframe detection

### 2. VideoRenderComponent

Renders video frames to a display or output target.

**Inputs:** `video_in` (VideoFrame packets)  
**Outputs:** None

**Configuration:**
```go
target := avflow.NewMockVideoRenderTarget(videoConfig)
component := avflow.NewVideoRenderComponent("video-render", target)
```

**Features:**
- Frame validation
- Resolution matching
- Frame counting
- Error handling

### 3. ObjectDetectionComponent

Detects objects in video frames using computer vision models.

**Inputs:** `video_in` (VideoFrame packets)  
**Outputs:** `detection_out` (ObjectDetectionResult packets)

**Configuration:**
```go
detectorConfig := avflow.ObjectDetectionConfig{
    ModelName:           "yolov8",
    ConfidenceThreshold: 0.5,
    NMSThreshold:        0.45,
    Classes:             []string{"person", "car", "dog", "cat"},
    MaxDetections:       100,
    UseGPU:              true,
}

detector := avflow.NewMockObjectDetector(detectorConfig)
component := avflow.NewObjectDetectionComponent("object-detect", detector)
```

**Output Format:**
```go
type ObjectDetectionResult struct {
    Detections    []Detection  // List of detected objects
    FrameNumber   int64
    Timestamp     int64
    ProcessingTime int32
}

type Detection struct {
    Box        BoundingBox
    Label      string
    Confidence float32
    Metadata   map[string]interface{}
}
```

**Supported Models:**
- YOLOv8 (recommended)
- YOLOv5
- Faster R-CNN
- SSD
- EfficientDet

### 4. FaceDetectionComponent

Detects faces in video frames with optional landmark detection.

**Inputs:** `video_in` (VideoFrame packets)  
**Outputs:** `face_out` (FaceDetectionResult packets)

**Configuration:**
```go
faceConfig := avflow.FaceDetectionConfig{
    ModelName:           "retinaface",
    ConfidenceThreshold: 0.7,
    DetectLandmarks:     true,
    EnableTracking:      true,
    MaxFaces:            10,
    UseGPU:              true,
}

detector := avflow.NewMockFaceDetector(faceConfig)
component := avflow.NewFaceDetectionComponent("face-detect", detector)
```

**Output Format:**
```go
type FaceDetectionResult struct {
    Faces          []FaceDetection
    FrameNumber    int64
    Timestamp      int64
    ProcessingTime int32
}

type FaceDetection struct {
    Box        BoundingBox
    Confidence float32
    Landmarks  map[string][2]float32  // Eyes, nose, mouth, etc.
    TrackID    int32
    Metadata   map[string]interface{}
}
```

**Supported Models:**
- RetinaFace (recommended)
- MTCNN
- SSD
- Cascade Classifier

### 5. EmotionDetectionComponent

Detects emotions from detected faces.

**Inputs:** `face_in` (FaceDetectionResult packets)  
**Outputs:** `emotion_out` (EmotionResult packets)

**Configuration:**
```go
emotionConfig := avflow.EmotionDetectionConfig{
    ModelName:           "affectnet",
    ConfidenceThreshold: 0.6,
    Emotions:            []string{"happy", "sad", "angry", "neutral", "surprised", "disgusted", "fearful"},
    UseGPU:              true,
}

detector := avflow.NewMockEmotionDetector(emotionConfig)
component := avflow.NewEmotionDetectionComponent("emotion-detect", detector)
```

**Output Format:**
```go
type EmotionResult struct {
    Emotion   string
    Scores    map[string]float32  // Confidence for each emotion
    FaceID    int32
    Timestamp time.Time
}
```

**Supported Emotions:**
- Happy
- Sad
- Angry
- Neutral
- Surprised
- Disgusted
- Fearful

## Video Data Types

### VideoFrame

```go
type VideoFrame struct {
    Data        []byte
    Width       int
    Height      int
    PixelFormat string
    Codec       string
    Timestamp   int64
    FrameNumber int64
    IsKeyFrame  bool
    Duration    int32
}
```

### BoundingBox

```go
type BoundingBox struct {
    X      int32
    Y      int32
    Width  int32
    Height int32
}
```

## Complete Multimodal Pipeline

### Architecture

```
Camera                          Microphone
    ↓                               ↓
[VideoCaptureComponent]     [MicComponent]
    ↓                               ↓
[ObjectDetectionComponent]  [VADComponent]
    ↓                               ↓
[FaceDetectionComponent]    [RealASRComponent]
    ↓                               ↓
[EmotionDetectionComponent] [ChatModelComponent]
    ↓                               ↓
    └───────────┬───────────────────┘
                ↓
        [ContextMergerComponent]
                ↓
        [RealTTSComponent]
                ↓
        [SpeakerComponent]
                ↓
        [VideoRenderComponent]
```

### Implementation Example

```go
// Create video components
videoCapture := avflow.NewMockVideoCaptureSource(videoConfig)
videoRender := avflow.NewMockVideoRenderTarget(videoConfig)
objectDetector := avflow.NewMockObjectDetector(objectConfig)
faceDetector := avflow.NewMockFaceDetector(faceConfig)
emotionDetector := avflow.NewMockEmotionDetector(emotionConfig)

// Create audio components
audioTransport := av.NewChanTransport("audio", "duplex", 64)
mic := avflow.NewMicComponent("mic", audioTransport)
vad := avflow.NewVADComponent("vad", 0.5)
speaker := avflow.NewSpeakerComponent("speaker", audioTransport)

// Create LLM component
llm := avflow.NewLLMComponent("llm", "Assistant: ", llmHandler)

// Build graph
g := avflow.NewGraph("multimodal-pipeline")

// Add components
g.AddComponent(avflow.NewVideoCaptureComponent("capture", videoCapture))
g.AddComponent(avflow.NewObjectDetectionComponent("detect-obj", objectDetector))
g.AddComponent(avflow.NewFaceDetectionComponent("detect-face", faceDetector))
g.AddComponent(avflow.NewEmotionDetectionComponent("detect-emotion", emotionDetector))
g.AddComponent(avflow.NewVideoRenderComponent("render", videoRender))
g.AddComponent(mic)
g.AddComponent(vad)
g.AddComponent(speaker)
g.AddComponent(llm)

// Connect video pipeline
g.Connect("capture", "video_out", "detect-obj", "video_in")
g.Connect("capture", "video_out", "detect-face", "video_in")
g.Connect("detect-face", "face_out", "detect-emotion", "face_in")
g.Connect("detect-obj", "detection_out", "render", "video_in")

// Connect audio pipeline
g.Connect("mic", "audio_out", "vad", "audio_in")
g.Connect("vad", "audio_out", "speaker", "audio_in")

// Run
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()
err := g.Run(ctx)
```

## Use Cases

### 1. Intelligent Video Conferencing

```
Participants' Cameras → Face Detection → Emotion Detection
                    ↓
            Adaptive UI Rendering
                    ↓
            Highlight engaged participants
```

### 2. Retail Analytics

```
Store Cameras → Object Detection → Customer Behavior Analysis
            ↓
    Heat Map Generation
            ↓
    Optimize Store Layout
```

### 3. Security Monitoring

```
Security Cameras → Object Detection → Alert System
                ↓
        Face Recognition
                ↓
    Identify Suspicious Persons
```

### 4. Emotion-Aware Chatbot

```
User Camera → Face Detection → Emotion Detection
                            ↓
                    Adjust Response Tone
                            ↓
                    Empathetic Responses
```

### 5. Accessibility Applications

```
User Camera → Face Detection → Eye Gaze Tracking
                            ↓
                    Control Interface
                            ↓
                    Hands-Free Navigation
```

## Performance Considerations

### Latency

| Component | Latency | Notes |
|-----------|---------|-------|
| Video Capture | <10ms | Depends on camera |
| Object Detection | 50-200ms | GPU accelerated |
| Face Detection | 20-100ms | GPU accelerated |
| Emotion Detection | 30-150ms | GPU accelerated |
| Video Render | <10ms | Depends on display |

### Memory Usage

| Component | Memory | Notes |
|-----------|--------|-------|
| 1280x720 Frame | ~1.3MB | YUV420P |
| Object Detection | 500MB-2GB | Model size |
| Face Detection | 100MB-500MB | Model size |
| Emotion Detection | 200MB-1GB | Model size |

### GPU Requirements

- **NVIDIA**: CUDA 11.0+, cuDNN 8.0+
- **AMD**: ROCm 4.0+
- **Intel**: oneAPI 2021.1+

## Best Practices

1. **Use GPU Acceleration**: Enable GPU for real-time performance
2. **Optimize Buffer Sizes**: Tune based on latency requirements
3. **Handle Errors Gracefully**: Implement fallback strategies
4. **Monitor Performance**: Track latency and throughput
5. **Test Locally First**: Use mock components for development
6. **Implement Backpressure**: Handle slow consumers
7. **Clean Up Resources**: Properly close components

## Troubleshooting

### High Latency

- Enable GPU acceleration
- Reduce frame resolution
- Increase buffer sizes
- Use faster models

### Memory Issues

- Reduce frame resolution
- Use quantized models
- Implement frame dropping
- Monitor memory usage

### Detection Accuracy

- Adjust confidence thresholds
- Use better models
- Improve lighting conditions
- Increase training data

## Future Enhancements

- [ ] OCR component for text extraction
- [ ] Pose estimation component
- [ ] Hand gesture recognition
- [ ] Scene understanding
- [ ] Real-time video effects
- [ ] 3D reconstruction
- [ ] Distributed processing
- [ ] Edge deployment

## Conclusion

The multimodal video components enable building sophisticated AI applications that combine audio and video processing. With the factory pattern for provider selection and the component-based architecture, you can easily build complex pipelines for various use cases.

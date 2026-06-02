package schema

// PartType identifies a segment inside a multimodal message.
type PartType string

const (
	PartTypeText  PartType = "text"
	PartTypeImage PartType = "image"
	PartTypeAudio PartType = "audio"
	PartTypeVideo PartType = "video"
	PartTypeFile  PartType = "file"
)

// MediaPart holds URL or inline base64 media referenced by a message part.
type MediaPart struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64data,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// InputPart is one user-side multimodal segment (text, image, audio, …).
type InputPart struct {
	Type  PartType   `json:"type"`
	Text  string     `json:"text,omitempty"`
	Media *MediaPart `json:"media,omitempty"`
	Extra map[string]any `json:"extra,omitempty"`
}

// OutputPart is one assistant-side multimodal segment.
type OutputPart struct {
	Type  PartType   `json:"type"`
	Text  string     `json:"text,omitempty"`
	Media *MediaPart `json:"media,omitempty"`
	Extra map[string]any `json:"extra,omitempty"`
}

// TextContent returns plain text for simple text-only parts.
func TextContent(parts []InputPart) string {
	var b string
	for _, p := range parts {
		if p.Type == PartTypeText {
			b += p.Text
		}
	}
	return b
}

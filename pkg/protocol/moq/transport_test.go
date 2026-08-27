package moq

import (
	"bytes"
	"testing"
)

// ─── Object 消息 ─────────────────────────────────────────────────────────────

func TestObjectMessageRoundtrip(t *testing.T) {
	original := &ObjectMessage{
		TrackAlias: 42,
		GroupID:    1,
		ObjectID:   100,
		Timestamp:  9999999,
		Payload:    []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}

	data := original.Encode()
	if len(data) == 0 {
		t.Fatal("encoded data is empty")
	}

	// 第一个字节应是消息类型 varint
	r := newBytesReader(data)
	msgType, err := readVarint(r)
	if err != nil {
		t.Fatalf("read message type: %v", err)
	}
	if MessageType(msgType) != MsgObject {
		t.Errorf("expected OBJECT type, got %d", msgType)
	}

	decoded, err := DecodeObjectMessage(r)
	if err != nil {
		t.Fatalf("decode object: %v", err)
	}
	if decoded.TrackAlias != original.TrackAlias {
		t.Errorf("track_alias: expected %d, got %d", original.TrackAlias, decoded.TrackAlias)
	}
	if decoded.GroupID != original.GroupID {
		t.Errorf("group_id: expected %d, got %d", original.GroupID, decoded.GroupID)
	}
	if decoded.ObjectID != original.ObjectID {
		t.Errorf("object_id: expected %d, got %d", original.ObjectID, decoded.ObjectID)
	}
	if decoded.Timestamp != original.Timestamp {
		t.Errorf("timestamp: expected %d, got %d", original.Timestamp, decoded.Timestamp)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Errorf("payload: expected %v, got %v", original.Payload, decoded.Payload)
	}
}

func TestObjectMessageEmptyPayload(t *testing.T) {
	original := &ObjectMessage{
		TrackAlias: 0,
		GroupID:    0,
		ObjectID:   0,
		Timestamp:  0,
		Payload:    []byte{},
	}

	data := original.Encode()
	r := newBytesReader(data)
	_, _ = readVarint(r) // skip type
	decoded, err := DecodeObjectMessage(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(decoded.Payload))
	}
}

func TestObjectMessageLargePayload(t *testing.T) {
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	original := &ObjectMessage{
		TrackAlias: 7,
		GroupID:    3,
		ObjectID:   2,
		Timestamp:  1234567890,
		Payload:    payload,
	}

	data := original.Encode()
	r := newBytesReader(data)
	_, _ = readVarint(r)
	decoded, err := DecodeObjectMessage(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Errorf("payload mismatch")
	}
}

// ─── Subscribe 消息 ──────────────────────────────────────────────────────────

func TestSubscribeMessageRoundtrip(t *testing.T) {
	original := &SubscribeMessage{
		SubscribeID:    5,
		TrackAlias:     10,
		TrackNamespace: "live.example.com",
		TrackName:      "video/track1",
		StartGroup:     0,
		EndGroup:       100,
	}

	data := original.Encode()
	r := newBytesReader(data)
	msgType, err := readVarint(r)
	if err != nil {
		t.Fatalf("read type: %v", err)
	}
	if MessageType(msgType) != MsgSubscribe {
		t.Errorf("expected SUBSCRIBE, got %d", msgType)
	}

	decoded, err := DecodeSubscribeMessage(r)
	if err != nil {
		t.Fatalf("decode subscribe: %v", err)
	}
	if decoded.SubscribeID != original.SubscribeID {
		t.Errorf("subscribe_id: expected %d, got %d", original.SubscribeID, decoded.SubscribeID)
	}
	if decoded.TrackAlias != original.TrackAlias {
		t.Errorf("track_alias: expected %d, got %d", original.TrackAlias, decoded.TrackAlias)
	}
	if decoded.TrackNamespace != original.TrackNamespace {
		t.Errorf("namespace: expected %s, got %s", original.TrackNamespace, decoded.TrackNamespace)
	}
	if decoded.TrackName != original.TrackName {
		t.Errorf("track_name: expected %s, got %s", original.TrackName, decoded.TrackName)
	}
	if decoded.StartGroup != original.StartGroup {
		t.Errorf("start_group: expected %d, got %d", original.StartGroup, decoded.StartGroup)
	}
	if decoded.EndGroup != original.EndGroup {
		t.Errorf("end_group: expected %d, got %d", original.EndGroup, decoded.EndGroup)
	}
}

// ─── Announce 消息 ───────────────────────────────────────────────────────────

func TestAnnounceMessageRoundtrip(t *testing.T) {
	original := &AnnounceMessage{
		TrackNamespace: "cdn.example.com",
		TrackNames:     []string{"audio/opus", "video/h264", "video/vp9"},
	}

	data := original.Encode()
	r := newBytesReader(data)
	msgType, err := readVarint(r)
	if err != nil {
		t.Fatalf("read type: %v", err)
	}
	if MessageType(msgType) != MsgAnnounce {
		t.Errorf("expected ANNOUNCE, got %d", msgType)
	}

	decoded, err := DecodeAnnounceMessage(r)
	if err != nil {
		t.Fatalf("decode announce: %v", err)
	}
	if decoded.TrackNamespace != original.TrackNamespace {
		t.Errorf("namespace: expected %s, got %s", original.TrackNamespace, decoded.TrackNamespace)
	}
	if len(decoded.TrackNames) != len(original.TrackNames) {
		t.Fatalf("track_names count: expected %d, got %d", len(original.TrackNames), len(decoded.TrackNames))
	}
	for i, name := range original.TrackNames {
		if decoded.TrackNames[i] != name {
			t.Errorf("track_names[%d]: expected %s, got %s", i, name, decoded.TrackNames[i])
		}
	}
}

func TestAnnounceMessageEmptyTracks(t *testing.T) {
	original := &AnnounceMessage{
		TrackNamespace: "empty.example.com",
		TrackNames:     []string{},
	}

	data := original.Encode()
	r := newBytesReader(data)
	_, _ = readVarint(r)
	decoded, err := DecodeAnnounceMessage(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.TrackNames) != 0 {
		t.Errorf("expected 0 track names, got %d", len(decoded.TrackNames))
	}
}

// ─── GoAway 消息 ─────────────────────────────────────────────────────────────

func TestGoAwayMessageRoundtrip(t *testing.T) {
	original := &GoAwayMessage{NewURI: "moq://new-server.example.com"}

	data := original.Encode()
	r := newBytesReader(data)
	msgType, err := readVarint(r)
	if err != nil {
		t.Fatalf("read type: %v", err)
	}
	if MessageType(msgType) != MsgGoAway {
		t.Errorf("expected GOAWAY, got %d", msgType)
	}

	decoded, err := DecodeGoAwayMessage(r)
	if err != nil {
		t.Fatalf("decode goaway: %v", err)
	}
	if decoded.NewURI != original.NewURI {
		t.Errorf("new_uri: expected %s, got %s", original.NewURI, decoded.NewURI)
	}
}

// ─── 消息帧读写 (EncodeMessage / ReadMessage) ────────────────────────────────

func TestEncodeAndReadMessage(t *testing.T) {
	original := &ObjectMessage{
		TrackAlias: 99,
		GroupID:    7,
		ObjectID:   3,
		Timestamp:  5000,
		Payload:    []byte("hello moq"),
	}

	var buf bytes.Buffer
	if err := EncodeMessage(&buf, original); err != nil {
		t.Fatalf("encode message: %v", err)
	}

	msgType, body, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	if msgType != MsgObject {
		t.Errorf("expected OBJECT, got %s", msgType)
	}

	m, err := DecodeMessageBody(msgType, body)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	obj := m.(*ObjectMessage)
	if obj.TrackAlias != original.TrackAlias {
		t.Errorf("track_alias: expected %d, got %d", original.TrackAlias, obj.TrackAlias)
	}
	if string(obj.Payload) != string(original.Payload) {
		t.Errorf("payload: expected %s, got %s", original.Payload, obj.Payload)
	}
}

func TestReadObjectStream(t *testing.T) {
	original := &ObjectMessage{
		TrackAlias: 1,
		GroupID:    2,
		ObjectID:   3,
		Timestamp:  4,
		Payload:    []byte("stream test"),
	}

	var buf bytes.Buffer
	if err := EncodeMessage(&buf, original); err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := ReadObject(&buf)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if decoded.TrackAlias != original.TrackAlias {
		t.Errorf("track_alias mismatch")
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Errorf("payload mismatch")
	}
}

func TestReadObjectWrongType(t *testing.T) {
	sub := &SubscribeMessage{
		SubscribeID:    1,
		TrackAlias:     2,
		TrackNamespace: "ns",
		TrackName:      "name",
		StartGroup:     0,
		EndGroup:       1,
	}

	var buf bytes.Buffer
	if err := EncodeMessage(&buf, sub); err != nil {
		t.Fatalf("encode: %v", err)
	}

	_, err := ReadObject(&buf)
	if err == nil {
		t.Fatal("expected error reading non-object as object")
	}
}

// ─── varint 辅助测试 ─────────────────────────────────────────────────────────

func TestAppendAndReadVarint(t *testing.T) {
	tests := []uint64{0, 1, 63, 64, 16383, 16384, 1073741823, 1073741824, 4611686018427387903}
	for _, v := range tests {
		b := appendVarint(nil, v)
		r := newBytesReader(b)
		got, err := readVarint(r)
		if err != nil {
			t.Errorf("read varint %d: %v", v, err)
			continue
		}
		if got != v {
			t.Errorf("varint roundtrip: expected %d, got %d", v, got)
		}
	}
}

func TestReadMessageEOF(t *testing.T) {
	_, _, err := ReadMessage(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error reading from empty reader")
	}
}

// ─── DecodeMessageBody 未知类型 ──────────────────────────────────────────────

func TestDecodeMessageBodyUnknownType(t *testing.T) {
	_, err := DecodeMessageBody(MessageType(0xFF), nil)
	if err == nil {
		t.Fatal("expected error for unknown message type")
	}
}

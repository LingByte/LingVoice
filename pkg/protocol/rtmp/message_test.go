package rtmp

import (
	"bytes"
	"testing"
)

func TestAMF0EncodeDecode(t *testing.T) {
	w := newAMFWriter()
	w.WriteString("connect")
	w.WriteNumber(1.0)
	w.WriteNull()
	w.WriteObject(map[string]AMFValue{
		"app":      {Type: AMFTypeString, Str: "live"},
		"tcUrl":    {Type: AMFTypeString, Str: "rtmp://localhost/live"},
		"flashVer": {Type: AMFTypeString, Str: "FMLE/3.0"},
	})

	values, err := DecodeAMF0(w.Bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(values) != 4 {
		t.Fatalf("expected 4 values, got %d", len(values))
	}

	s, ok := amfString(values[0])
	if !ok || s != "connect" {
		t.Fatalf("values[0] = %v, want connect", values[0])
	}

	n, ok := amfNumber(values[1])
	if !ok || n != 1.0 {
		t.Fatalf("values[1] = %v, want 1.0", values[1])
	}

	if values[2].Type != AMFTypeNull {
		t.Fatalf("values[2] type = %d, want null", values[2].Type)
	}

	obj, ok := amfObject(values[3])
	if !ok {
		t.Fatalf("values[3] is not object")
	}
	if app, ok := obj["app"]; ok {
		if s, ok := amfString(app); !ok || s != "live" {
			t.Fatalf("app = %v, want live", app)
		}
	} else {
		t.Fatal("missing app property")
	}
}

func TestAMF0Boolean(t *testing.T) {
	w := newAMFWriter()
	w.WriteBoolean(true)
	w.WriteBoolean(false)

	values, err := DecodeAMF0(w.Bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if b, _ := amfBool(values[0]); !b {
		t.Fatal("values[0] should be true")
	}
	if b, _ := amfBool(values[1]); b {
		t.Fatal("values[1] should be false")
	}
}

func TestConnectResultProps(t *testing.T) {
	props := ConnectResultProps()
	if props["fmsVer"].Str != "FMS/3,0,1,123" {
		t.Fatalf("fmsVer = %v", props["fmsVer"])
	}
	info := ConnectResultInfo()
	if info["code"].Str != "NetConnection.Connect.Success" {
		t.Fatalf("code = %v", info["code"])
	}
}

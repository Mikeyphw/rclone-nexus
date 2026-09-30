package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeGoldenRequest(t *testing.T) {
	input := `{"schema_version":1,"request_id":"req-1","client":{"name":"test","version":"1","protocol":{"min":1,"max":1}},"operation":{"name":"provider.status","class":"query","args":{}}}`
	request, machineError := DecodeRequest(strings.NewReader(input))
	if machineError != nil {
		t.Fatalf("unexpected error: %+v", machineError)
	}
	if request.RequestID != "req-1" || request.Operation.Name != "provider.status" {
		t.Fatalf("unexpected request: %+v", request)
	}
	if selected, err := Negotiate(request); err != nil || selected != 1 {
		t.Fatalf("negotiation failed: selected=%d err=%+v", selected, err)
	}
}

func TestDecodeRejectsUnknownEnvelopeField(t *testing.T) {
	input := `{"schema_version":1,"request_id":"req-1","client":{"protocol":{"min":1,"max":1}},"operation":{"name":"provider.status","class":"query","args":{}},"argv":["sh","-c","id"]}`
	_, machineError := DecodeRequest(strings.NewReader(input))
	if machineError == nil || machineError.Code != "invalid_request" {
		t.Fatalf("expected invalid_request, got %+v", machineError)
	}
}

func TestNegotiationRejectsMismatch(t *testing.T) {
	request := NewRequest("req-2", "provider.status", ClassQuery, struct{}{})
	request.Client.Protocol.Min = 9
	request.Client.Protocol.Max = 10
	_, machineError := Negotiate(request)
	if machineError == nil || machineError.Code != "unsupported_protocol" {
		t.Fatalf("expected unsupported_protocol, got %+v", machineError)
	}
}

func TestResponseRedactsAndBoundsSensitiveValues(t *testing.T) {
	response := Response{
		SchemaVersion: SchemaVersion,
		Kind:          "response",
		RequestID:     "r",
		Protocol:      1,
		OK:            true,
		Result: map[string]any{
			"access_token": "super-secret",
			"nested":       map[string]any{"password": "hunter2", "message": strings.Repeat("x", MaxStringBytes+4096)},
		},
	}
	payload, err := MarshalResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("super-secret")) || bytes.Contains(payload, []byte("hunter2")) {
		t.Fatalf("secret leaked: %s", payload)
	}
	var decoded Response
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	result, ok := decoded.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", decoded.Result)
	}
	if result["access_token"] != "<redacted>" {
		t.Fatalf("token not redacted: %#v", result["access_token"])
	}
	nested, ok := result["nested"].(map[string]any)
	if !ok || nested["password"] != "<redacted>" || !strings.Contains(nested["message"].(string), "<truncated>") {
		t.Fatalf("nested redaction/bounds absent")
	}
	if len(payload) > MaxResponseBytes {
		t.Fatalf("response exceeded bound: %d", len(payload))
	}
}

func TestEventPayloadBound(t *testing.T) {
	event := Event{SchemaVersion: 1, Kind: "event", RequestID: "r", Protocol: 1, Sequence: 1, Event: "progress", Data: map[string]any{"blob": strings.Repeat("a", MaxEventBytes*2)}}
	payload, err := MarshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > MaxEventBytes {
		t.Fatalf("event exceeded bound: %d", len(payload))
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestRequestSizeBound(t *testing.T) {
	oversize := bytes.Repeat([]byte{'x'}, MaxRequestBytes+1)
	_, machineError := DecodeRequest(bytes.NewReader(oversize))
	if machineError == nil || machineError.Code != "payload_too_large" {
		t.Fatalf("expected payload_too_large, got %+v", machineError)
	}
}

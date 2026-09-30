package protocol

import (
	"encoding/json"
	"fmt"
	"io"

	"rclone-nexus/internal/redact"
)

func sanitizeResponse(response Response) Response {
	response.Result = redact.Value(response.Result, MaxStringBytes)
	if response.Error != nil {
		copy := *response.Error
		copy.Message = redact.BoundedString(copy.Message, MaxStringBytes)
		copy.Detail = redact.BoundedString(copy.Detail, MaxStringBytes)
		response.Error = &copy
	}
	return response
}

func sanitizeEvent(event Event) Event {
	event.Message = redact.BoundedString(event.Message, MaxStringBytes)
	event.Data = redact.Value(event.Data, MaxStringBytes)
	return event
}

func MarshalResponse(response Response) ([]byte, error) {
	response = sanitizeResponse(response)
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if len(payload) <= MaxResponseBytes {
		return payload, nil
	}
	fallback := Response{
		SchemaVersion: SchemaVersion,
		Kind:          "response",
		RequestID:     response.RequestID,
		Protocol:      response.Protocol,
		OK:            false,
		Error:         Error("payload_too_large", "response exceeded protocol limit", fmt.Sprintf("maximum %d bytes", MaxResponseBytes)),
	}
	return json.Marshal(fallback)
}

func MarshalEvent(event Event) ([]byte, error) {
	event = sanitizeEvent(event)
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(payload) <= MaxEventBytes {
		return payload, nil
	}
	event.Message = "event payload truncated"
	event.Data = map[string]any{"truncated": true}
	payload, err = json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxEventBytes {
		return nil, fmt.Errorf("bounded event still exceeds limit")
	}
	return payload, nil
}

func WriteNDJSON(writer io.Writer, payload []byte) error {
	if _, err := writer.Write(payload); err != nil {
		return err
	}
	_, err := writer.Write([]byte{'\n'})
	return err
}

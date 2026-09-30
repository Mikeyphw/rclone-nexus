package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"

	"rclone-nexus/internal/buildinfo"
	"rclone-nexus/internal/redact"
)

const (
	SchemaVersion      = 1
	MaxRequestBytes    = 1 << 20
	MaxResponseBytes   = 1 << 20
	MaxEventBytes      = 256 << 10
	MaxStringBytes     = 64 << 10
	MaxRequestIDLength = 128
)

const (
	ClassQuery     = "query"
	ClassPreview   = "preview"
	ClassRun       = "run"
	ClassCancel    = "cancel"
	ClassReconcile = "reconcile"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

type ClientProtocol struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type ClientInfo struct {
	Name     string         `json:"name,omitempty"`
	Version  string         `json:"version,omitempty"`
	Protocol ClientProtocol `json:"protocol"`
}

type OperationRequest struct {
	Name  string          `json:"name"`
	Class string          `json:"class"`
	Args  json.RawMessage `json:"args,omitempty"`
}

type Request struct {
	SchemaVersion int              `json:"schema_version"`
	RequestID     string           `json:"request_id"`
	Client        ClientInfo       `json:"client"`
	Operation     OperationRequest `json:"operation"`
}

type MachineError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type Response struct {
	SchemaVersion int           `json:"schema_version"`
	Kind          string        `json:"kind"`
	RequestID     string        `json:"request_id"`
	Protocol      int           `json:"protocol"`
	OK            bool          `json:"ok"`
	Result        any           `json:"result,omitempty"`
	Error         *MachineError `json:"error,omitempty"`
	Capabilities  *Capabilities `json:"capabilities,omitempty"`
}

type Event struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	RequestID     string `json:"request_id"`
	Protocol      int    `json:"protocol"`
	Sequence      int    `json:"sequence"`
	Event         string `json:"event"`
	Message       string `json:"message,omitempty"`
	Data          any    `json:"data,omitempty"`
}

type OperationDescriptor struct {
	Name        string `json:"name"`
	Class       string `json:"class"`
	Description string `json:"description"`
	Cancellable bool   `json:"cancellable,omitempty"`
}

type Limits struct {
	MaxRequestBytes  int `json:"max_request_bytes"`
	MaxResponseBytes int `json:"max_response_bytes"`
	MaxEventBytes    int `json:"max_event_bytes"`
	MaxStringBytes   int `json:"max_string_bytes"`
}

type Capabilities struct {
	SchemaVersion int                   `json:"schema_version"`
	Server        map[string]string     `json:"server"`
	Protocol      map[string]int        `json:"protocol"`
	Limits        Limits                `json:"limits"`
	Classes       []string              `json:"operation_classes"`
	Operations    []OperationDescriptor `json:"operations"`
}

func NewRequest(id, name, class string, args any) Request {
	raw := json.RawMessage(`{}`)
	if args != nil {
		encoded, err := json.Marshal(args)
		if err == nil {
			raw = encoded
		}
	}
	return Request{
		SchemaVersion: SchemaVersion,
		RequestID:     id,
		Client: ClientInfo{
			Name:    "racctl",
			Version: buildinfo.Version,
			Protocol: ClientProtocol{
				Min: buildinfo.ProtocolMin,
				Max: buildinfo.ProtocolMax,
			},
		},
		Operation: OperationRequest{Name: name, Class: class, Args: raw},
	}
}

func DecodeRequest(reader io.Reader) (Request, *MachineError) {
	limited := io.LimitReader(reader, MaxRequestBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return Request{}, Error("invalid_request", "could not read request", err.Error())
	}
	if len(payload) > MaxRequestBytes {
		return Request{}, Error("payload_too_large", "request exceeds protocol limit", fmt.Sprintf("maximum %d bytes", MaxRequestBytes))
	}
	var request Request
	decoder := json.NewDecoder(bytesReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, Error("invalid_request", "invalid JSON request envelope", err.Error())
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Request{}, Error("invalid_request", "request must contain exactly one JSON object", "")
	}
	if request.SchemaVersion != SchemaVersion {
		return Request{}, Error("unsupported_schema", "unsupported request schema", fmt.Sprintf("supported schema_version=%d", SchemaVersion))
	}
	if request.RequestID == "" || len(request.RequestID) > MaxRequestIDLength || !requestIDPattern.MatchString(request.RequestID) {
		return Request{}, Error("invalid_request_id", "request_id is invalid", "use 1-128 characters from A-Z a-z 0-9 . _ : -")
	}
	if request.Operation.Name == "" || request.Operation.Class == "" {
		return Request{}, Error("invalid_request", "operation name and class are required", "")
	}
	if len(request.Operation.Args) == 0 {
		request.Operation.Args = json.RawMessage(`{}`)
	}
	return request, nil
}

// tiny byte reader avoids exporting protocol parsing implementation details.
type byteSliceReader struct {
	data []byte
	off  int
}

func bytesReader(data []byte) *byteSliceReader { return &byteSliceReader{data: data} }
func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

func Error(code, message, detail string) *MachineError {
	return &MachineError{
		Code:    code,
		Message: redact.BoundedString(message, MaxStringBytes),
		Detail:  redact.BoundedString(detail, MaxStringBytes),
	}
}

func Negotiate(request Request) (int, *MachineError) {
	minimum := request.Client.Protocol.Min
	maximum := request.Client.Protocol.Max
	if minimum <= 0 || maximum <= 0 || minimum > maximum {
		return 0, Error("invalid_protocol_range", "client protocol range is invalid", "")
	}
	if maximum < buildinfo.ProtocolMin || minimum > buildinfo.ProtocolMax {
		return 0, Error(
			"unsupported_protocol",
			"client and server protocol ranges do not overlap",
			fmt.Sprintf("server=%d-%d client=%d-%d", buildinfo.ProtocolMin, buildinfo.ProtocolMax, minimum, maximum),
		)
	}
	selected := maximum
	if selected > buildinfo.ProtocolMax {
		selected = buildinfo.ProtocolMax
	}
	if selected < buildinfo.ProtocolMin {
		selected = buildinfo.ProtocolMin
	}
	return selected, nil
}

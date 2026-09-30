package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

type Result struct {
	Response protocol.Response
	Events   []protocol.Event
}

func Execute(ctx context.Context, p paths.Paths, engine *control.Engine, request protocol.Request) Result {
	if os.Getenv("RNEXUS_RACD_DISABLE") != "1" {
		if result, err := executeRemote(ctx, p, request); err == nil {
			return result
		}
	}
	return executeLocal(ctx, engine, request)
}

func executeLocal(ctx context.Context, engine *control.Engine, request protocol.Request) Result {
	result := Result{}
	sequence := 0
	result.Response = engine.Execute(ctx, request, func(event, message string, data any) {
		sequence++
		result.Events = append(result.Events, protocol.Event{
			SchemaVersion: protocol.SchemaVersion, Kind: "event", RequestID: request.RequestID,
			Protocol: protocolVersion(request), Sequence: sequence, Event: event, Message: message, Data: data,
		})
	})
	return result
}

func executeRemote(ctx context.Context, p paths.Paths, request protocol.Request) (Result, error) {
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "unix", p.Socket)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	payload, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	if len(payload) > protocol.MaxRequestBytes {
		return Result{}, errors.New("request too large")
	}
	if err := protocol.WriteNDJSON(conn, payload); err != nil {
		return Result{}, err
	}

	result := Result{}
	scanner := bufio.NewScanner(io.LimitReader(conn, 4*protocol.MaxResponseBytes))
	scanner.Buffer(make([]byte, 64<<10), protocol.MaxResponseBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			return Result{}, err
		}
		switch header.Kind {
		case "event":
			var event protocol.Event
			if err := json.Unmarshal(line, &event); err != nil {
				return Result{}, err
			}
			result.Events = append(result.Events, event)
		case "response":
			if err := json.Unmarshal(line, &result.Response); err != nil {
				return Result{}, err
			}
			return result, nil
		default:
			return Result{}, errors.New("unexpected daemon envelope")
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, err
	}
	return Result{}, errors.New("daemon closed without response")
}

func ProtocolStream(ctx context.Context, p paths.Paths, engine *control.Engine, request protocol.Request, writer io.Writer) error {
	if os.Getenv("RNEXUS_RACD_DISABLE") != "1" {
		if err := streamRemote(ctx, p, request, writer); err == nil {
			return nil
		}
	}
	sequence := 0
	response := engine.Execute(ctx, request, func(event, message string, data any) {
		sequence++
		payload, err := protocol.MarshalEvent(protocol.Event{
			SchemaVersion: protocol.SchemaVersion, Kind: "event", RequestID: request.RequestID,
			Protocol: protocolVersion(request), Sequence: sequence, Event: event, Message: message, Data: data,
		})
		if err == nil {
			_ = protocol.WriteNDJSON(writer, payload)
		}
	})
	payload, err := protocol.MarshalResponse(response)
	if err != nil {
		return err
	}
	return protocol.WriteNDJSON(writer, payload)
}

func streamRemote(ctx context.Context, p paths.Paths, request protocol.Request, writer io.Writer) error {
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "unix", p.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if err := protocol.WriteNDJSON(conn, payload); err != nil {
		return err
	}
	_, err = io.Copy(writer, conn)
	return err
}

func protocolVersion(request protocol.Request) int {
	selected, _ := protocol.Negotiate(request)
	return selected
}

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"rclone-nexus/internal/buildinfo"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/provider"
)

type Emitter func(event, message string, data any)

type Handler func(context.Context, *Engine, json.RawMessage, Emitter) (any, *protocol.MachineError)

type operation struct {
	descriptor protocol.OperationDescriptor
	handler    Handler
}

type Engine struct {
	Paths    paths.Paths
	activeMu sync.Mutex
	active   map[string]context.CancelFunc
	ops      map[string]operation
}

func New(p paths.Paths) *Engine {
	engine := &Engine{Paths: p, active: map[string]context.CancelFunc{}, ops: map[string]operation{}}
	engine.register("provider.status", protocol.ClassQuery, "Inspect provider/rclone/FUSE/config readiness", providerStatus)
	engine.register("config.snapshot", protocol.ClassQuery, "Read the credential-free configuration registry", configSnapshot)
	engine.register("config.preview", protocol.ClassPreview, "Validate a full candidate registry and preview its diff", configPreview)
	engine.register("config.apply", protocol.ClassRun, "Atomically publish a revision-bound candidate registry", configApply)
	engine.register("config.previous", protocol.ClassQuery, "Read previous-known-good configuration metadata", configPrevious)
	engine.register("config.rollback.preview", protocol.ClassPreview, "Preview rollback to previous-known-good configuration", configRollbackPreview)
	engine.register("config.rollback", protocol.ClassRun, "Rollback to previous-known-good configuration", configRollback)
	engine.register("mount.list", protocol.ClassQuery, "List configured mount names", mountList)
	engine.register("mount.status", protocol.ClassQuery, "Read one or all managed mount states", mountStatus)
	engine.register("mount.start.preview", protocol.ClassPreview, "Validate and preview a mount start", mountStartPreview)
	engine.register("mount.start", protocol.ClassRun, "Start one configured mount", mountStart)
	engine.register("mount.stop", protocol.ClassRun, "Stop one managed mount", mountStop)
	engine.register("mount.restart", protocol.ClassRun, "Restart one managed mount", mountRestart)
	engine.register("operation.cancel", protocol.ClassCancel, "Cancel one active request by request_id", operationCancel)
	engine.register("mount.reconcile", protocol.ClassReconcile, "Start enabled mounts that are not running", mountReconcile)
	return engine
}

func (e *Engine) register(name, class, description string, handler Handler) {
	e.ops[name] = operation{descriptor: protocol.OperationDescriptor{Name: name, Class: class, Description: description}, handler: handler}
}

func (e *Engine) Capabilities() protocol.Capabilities {
	operations := make([]protocol.OperationDescriptor, 0, len(e.ops))
	for _, op := range e.ops {
		operations = append(operations, op.descriptor)
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].Name < operations[j].Name })
	return protocol.Capabilities{
		SchemaVersion: protocol.SchemaVersion,
		Server:        map[string]string{"name": buildinfo.Name, "binary": buildinfo.Binary, "version": buildinfo.Version},
		Protocol:      map[string]int{"min": buildinfo.ProtocolMin, "max": buildinfo.ProtocolMax},
		Limits:        protocol.Limits{MaxRequestBytes: protocol.MaxRequestBytes, MaxResponseBytes: protocol.MaxResponseBytes, MaxEventBytes: protocol.MaxEventBytes, MaxStringBytes: protocol.MaxStringBytes},
		Classes:       []string{protocol.ClassQuery, protocol.ClassPreview, protocol.ClassRun, protocol.ClassCancel, protocol.ClassReconcile},
		Operations:    operations,
	}
}

func (e *Engine) Execute(ctx context.Context, request protocol.Request, emit Emitter) protocol.Response {
	selected, negotiationError := protocol.Negotiate(request)
	if negotiationError != nil {
		return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: 0, OK: false, Error: negotiationError, Capabilities: ptrCapabilities(e.Capabilities())}
	}
	op, ok := e.ops[request.Operation.Name]
	if !ok {
		return failure(request, selected, "unknown_operation", "operation is not allow-listed", request.Operation.Name)
	}
	if request.Operation.Class != op.descriptor.Class {
		return failure(request, selected, "operation_class_mismatch", "operation class does not match registry", fmt.Sprintf("expected=%s got=%s", op.descriptor.Class, request.Operation.Class))
	}

	operationContext := ctx
	var cancel context.CancelFunc
	if op.descriptor.Class == protocol.ClassRun || op.descriptor.Class == protocol.ClassReconcile {
		operationContext, cancel = context.WithCancel(ctx)
		e.activeMu.Lock()
		if _, exists := e.active[request.RequestID]; exists {
			e.activeMu.Unlock()
			cancel()
			return failure(request, selected, "duplicate_request_id", "an operation with this request_id is already active", "")
		}
		e.active[request.RequestID] = cancel
		e.activeMu.Unlock()
		defer func() {
			e.activeMu.Lock()
			delete(e.active, request.RequestID)
			e.activeMu.Unlock()
			cancel()
		}()
	}

	result, machineError := op.handler(operationContext, e, request.Operation.Args, emit)
	if machineError != nil {
		return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: selected, OK: false, Error: machineError}
	}
	return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: selected, OK: true, Result: result}
}

func (e *Engine) Cancel(requestID string) bool {
	e.activeMu.Lock()
	cancel := e.active[requestID]
	e.activeMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func ptrCapabilities(value protocol.Capabilities) *protocol.Capabilities { return &value }

func failure(request protocol.Request, selected int, code, message, detail string) protocol.Response {
	return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: selected, OK: false, Error: protocol.Error(code, message, detail)}
}

func strictArgs(raw json.RawMessage, out any) *protocol.MachineError {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return protocol.Error("invalid_argument", "operation arguments are invalid", err.Error())
	}
	return nil
}

func mapError(err error) *protocol.MachineError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return protocol.Error("operation_cancelled", "operation was cancelled", "")
	}
	message := err.Error()
	switch {
	case mounts.IsStaleRevision(err):
		return protocol.Error("stale_revision", "configuration revision is stale", message)
	case mounts.IsCandidateDigestMismatch(err):
		return protocol.Error("candidate_digest_mismatch", "candidate digest does not match preview", message)
	case mounts.IsPreviousUnavailable(err):
		return protocol.Error("previous_config_unavailable", "previous-known-good configuration is unavailable", message)
	case strings.Contains(message, "mountpoints overlap"):
		return protocol.Error("mountpoint_overlap", "mountpoints overlap", message)
	case strings.Contains(message, "unsupported vfs_cache_mode") || strings.Contains(message, "invalid vfs_") || strings.Contains(message, "invalid dir_cache_time") || strings.Contains(message, "invalid poll_interval") || strings.Contains(message, "unsupported log_level"):
		return protocol.Error("invalid_mount_config", "mount configuration is invalid", message)
	case strings.Contains(message, "invalid mount name"):
		return protocol.Error("invalid_mount_name", "invalid mount name", message)
	case strings.Contains(message, "mount definition not found"):
		return protocol.Error("mount_not_found", "mount definition not found", message)
	case strings.Contains(message, "rclone binary not found"):
		return protocol.Error("provider_unavailable", "rclone provider is unavailable", message)
	case strings.Contains(message, "rclone config not found"):
		return protocol.Error("provider_config_missing", "rclone configuration is unavailable", message)
	default:
		return protocol.Error("operation_failed", "operation failed", message)
	}
}

func providerStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	return provider.Discover(engine.Paths), nil
}

func configSnapshot(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	snapshot, err := mounts.PublicSnapshot(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return snapshot, nil
}

func configPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Mounts []mounts.CandidateConfig `json:"mounts"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	preview, err := mounts.PreviewCandidate(engine.Paths, args.Mounts)
	if err != nil {
		return nil, mapError(err)
	}
	return preview, nil
}

func configApply(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64                   `json:"expected_revision"`
		CandidateDigest  string                   `json:"candidate_digest"`
		Mounts           []mounts.CandidateConfig `json:"mounts"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	report, err := mounts.ApplyCandidate(ctx, engine.Paths, args.ExpectedRevision, args.CandidateDigest, args.Mounts, func(name, state string) {
		if emit != nil {
			emit("progress", "configuration lifecycle action", map[string]any{"name": name, "state": state})
		}
	})
	if err != nil {
		return nil, mapError(err)
	}
	return report, nil
}

func configPrevious(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	previous, err := mounts.LoadPreviousRegistry(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	preview, err := mounts.PreviewPrevious(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{
		"revision": previous.Revision,
		"digest":   previous.Digest,
		"mounts":   preview.Mounts,
	}, nil
}

func configRollbackPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	preview, err := mounts.PreviewPrevious(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return preview, nil
}

func configRollback(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64 `json:"expected_revision"`
		PreviousDigest   string `json:"previous_digest"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	report, err := mounts.RollbackPrevious(ctx, engine.Paths, args.ExpectedRevision, args.PreviousDigest, func(name, state string) {
		if emit != nil {
			emit("progress", "configuration rollback lifecycle action", map[string]any{"name": name, "state": state})
		}
	})
	if err != nil {
		return nil, mapError(err)
	}
	return report, nil
}

func mountList(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	names, err := mounts.List(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"mounts": names}, nil
}

func mountStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name != "" {
		status := mounts.StatusOne(engine.Paths, args.Name)
		return status, nil
	}
	statuses, err := mounts.StatusAll(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"mounts": statuses}, nil
}

func mountStartPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	preview, err := mounts.PreviewStart(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return preview, nil
}

func mountStart(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if emit != nil {
		emit("progress", "starting mount", map[string]any{"name": args.Name})
	}
	result, err := mounts.Start(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	if emit != nil {
		emit("progress", "mount start complete", map[string]any{"name": args.Name, "state": result.State})
	}
	return result, nil
}

func mountStop(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if emit != nil {
		emit("progress", "stopping mount", map[string]any{"name": args.Name})
	}
	result, err := mounts.Stop(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func mountRestart(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if emit != nil {
		emit("progress", "restarting mount", map[string]any{"name": args.Name})
	}
	result, err := mounts.Restart(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func operationCancel(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		RequestID string `json:"request_id"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.RequestID == "" {
		return nil, protocol.Error("invalid_argument", "request_id is required", "")
	}
	if !engine.Cancel(args.RequestID) {
		return nil, protocol.Error("operation_not_found", "active operation was not found", args.RequestID)
	}
	return map[string]any{"request_id": args.RequestID, "cancelled": true}, nil
}

func mountReconcile(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Boot bool `json:"boot,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	report, err := mounts.Reconcile(ctx, engine.Paths, func(name, state string) {
		if emit != nil {
			emit("progress", "reconcile mount", map[string]any{"name": name, "state": state})
		}
	})
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"boot": args.Boot, "changed": report.Changed, "failures": report.Failures}, nil
}

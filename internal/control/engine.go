package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"rclone-nexus/internal/buildinfo"
	"rclone-nexus/internal/journal"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/namespace"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/supervisor"
)

type Emitter func(event, message string, data any)

type Handler func(context.Context, *Engine, json.RawMessage, Emitter) (any, *protocol.MachineError)

type operation struct {
	descriptor  protocol.OperationDescriptor
	handler     Handler
	cancellable bool
}

type activeOperation struct {
	cancel      context.CancelFunc
	cancellable bool
}

type Engine struct {
	Paths    paths.Paths
	activeMu sync.Mutex
	active   map[string]activeOperation
	ops      map[string]operation
}

func New(p paths.Paths) *Engine {
	engine := &Engine{Paths: p, active: map[string]activeOperation{}, ops: map[string]operation{}}
	engine.register("provider.status", protocol.ClassQuery, "Inspect provider/rclone/FUSE/config readiness", providerStatus)
	engine.register("config.snapshot", protocol.ClassQuery, "Read the credential-free configuration registry", configSnapshot)
	engine.register("config.preview", protocol.ClassPreview, "Validate a full candidate registry and preview its diff", configPreview)
	engine.register("config.apply", protocol.ClassRun, "Atomically publish a revision-bound candidate registry", configApply)
	engine.register("config.previous", protocol.ClassQuery, "Read previous-known-good configuration metadata", configPrevious)
	engine.register("config.rollback.preview", protocol.ClassPreview, "Preview rollback to previous-known-good configuration", configRollbackPreview)
	engine.register("config.rollback", protocol.ClassRun, "Rollback to previous-known-good configuration", configRollback)
	engine.register("mount.list", protocol.ClassQuery, "List configured mount names", mountList)
	engine.register("mount.status", protocol.ClassQuery, "Read one or all managed mount states", mountStatus)
	engine.register("mount.health", protocol.ClassQuery, "Read supervisor health/readiness state", mountHealth)
	engine.register("namespace.inspect", protocol.ClassQuery, "Inspect Android mount namespace topology and observed visibility", namespaceInspect)
	engine.register("namespace.preview", protocol.ClassPreview, "Preview app-visibility namespace mutations", namespacePreview)
	engine.register("namespace.apply", protocol.ClassRun, "Apply app-visibility namespace mutations transactionally", namespaceApply)
	engine.register("namespace.rollback.preview", protocol.ClassPreview, "Preview release of Nexus-owned namespace binds", namespaceRollbackPreview)
	engine.register("namespace.rollback", protocol.ClassRun, "Release Nexus-owned namespace binds and disable app visibility", namespaceRollback)
	engine.register("namespace.reconcile", protocol.ClassReconcile, "Reconcile persisted app-visibility intent after namespace churn", namespaceReconcile)
	engine.register("operation.status", protocol.ClassQuery, "Read persistent operation journal state", operationStatus)
	engine.register("operation.list", protocol.ClassQuery, "List persistent operation journal state", operationList)
	engine.register("mount.start.preview", protocol.ClassPreview, "Validate and preview a mount start", mountStartPreview)
	engine.registerCancellable("mount.start", protocol.ClassRun, "Start one configured mount", mountStart)
	engine.registerCancellable("mount.stop", protocol.ClassRun, "Stop one managed mount", mountStop)
	engine.registerCancellable("mount.restart", protocol.ClassRun, "Restart one managed mount", mountRestart)
	engine.register("operation.cancel", protocol.ClassCancel, "Cancel one active request by request_id", operationCancel)
	engine.registerCancellable("mount.reconcile", protocol.ClassReconcile, "Reconcile desired mounts with readiness/recovery state", mountReconcile)
	return engine
}

func (e *Engine) register(name, class, description string, handler Handler) {
	e.ops[name] = operation{descriptor: protocol.OperationDescriptor{Name: name, Class: class, Description: description}, handler: handler}
}

func (e *Engine) registerCancellable(name, class, description string, handler Handler) {
	e.ops[name] = operation{descriptor: protocol.OperationDescriptor{Name: name, Class: class, Description: description, Cancellable: true}, handler: handler, cancellable: true}
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

	journaled := op.descriptor.Class == protocol.ClassRun || op.descriptor.Class == protocol.ClassReconcile
	operationContext := ctx
	var cancel context.CancelFunc
	if journaled {
		if _, err := journal.Begin(e.Paths, request.RequestID, request.Operation.Name, request.Operation.Class, op.cancellable); err != nil {
			code := "operation_journal_unavailable"
			if strings.Contains(err.Error(), "already contains request_id") {
				code = "duplicate_request_id"
			}
			return failure(request, selected, code, "operation could not enter the persistent journal", err.Error())
		}
		if op.cancellable {
			operationContext, cancel = context.WithCancel(ctx)
		} else {
			operationContext = context.WithoutCancel(ctx)
		}
		e.activeMu.Lock()
		if _, exists := e.active[request.RequestID]; exists {
			e.activeMu.Unlock()
			if cancel != nil {
				cancel()
			}
			machineErr := protocol.Error("duplicate_request_id", "an operation with this request_id is already active", "")
			_ = journal.Complete(e.Paths, request.RequestID, journal.StateFailed, nil, machineErr)
			return failure(request, selected, machineErr.Code, machineErr.Message, machineErr.Detail)
		}
		e.active[request.RequestID] = activeOperation{cancel: cancel, cancellable: op.cancellable}
		e.activeMu.Unlock()
		defer func() {
			e.activeMu.Lock()
			delete(e.active, request.RequestID)
			e.activeMu.Unlock()
			if cancel != nil {
				cancel()
			}
		}()
	}

	wrappedEmit := emit
	if journaled {
		wrappedEmit = func(event, message string, data any) {
			_ = journal.Append(e.Paths, request.RequestID, event, message, data)
			if emit != nil {
				emit(event, message, data)
			}
		}
	}
	result, machineError := op.handler(operationContext, e, request.Operation.Args, wrappedEmit)
	if machineError != nil {
		if journaled {
			state := journal.StateFailed
			if machineError.Code == "operation_cancelled" {
				state = journal.StateCancelled
			}
			_ = journal.Complete(e.Paths, request.RequestID, state, nil, machineError)
		}
		return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: selected, OK: false, Error: machineError}
	}
	if journaled {
		_ = journal.Complete(e.Paths, request.RequestID, journal.StateSucceeded, result, nil)
	}
	return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: selected, OK: true, Result: result}
}

func (e *Engine) Cancel(requestID string) (found, cancellable, cancelled bool) {
	e.activeMu.Lock()
	active, ok := e.active[requestID]
	e.activeMu.Unlock()
	if !ok {
		return false, false, false
	}
	if !active.cancellable || active.cancel == nil {
		return true, false, false
	}
	active.cancel()
	return true, true, true
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
	case strings.Contains(message, "namespace strategy unsupported"):
		return protocol.Error("namespace_strategy_unsupported", "no qualified namespace visibility strategy is available", message)
	case strings.Contains(message, "namespace ownership mismatch"):
		return protocol.Error("namespace_ownership_mismatch", "namespace mount no longer matches Nexus ownership evidence", message)
	case strings.Contains(message, "target mountpoint unavailable") || strings.Contains(message, "mount namespace"):
		return protocol.Error("namespace_target_unavailable", "target mount namespace is unavailable", message)
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
	preview, previewErr := mounts.PreviewCandidate(engine.Paths, args.Mounts)
	if previewErr != nil {
		return nil, mapError(previewErr)
	}
	suspended := make([]string, 0, len(preview.RequiresRestart))
	for _, name := range preview.RequiresRestart {
		if namespace.Desired(engine.Paths, name) {
			if _, suspendErr := namespace.SuspendOwned(ctx, engine.Paths, name); suspendErr != nil {
				return nil, mapError(suspendErr)
			}
			suspended = append(suspended, name)
		}
	}
	report, err := mounts.ApplyCandidate(ctx, engine.Paths, args.ExpectedRevision, args.CandidateDigest, args.Mounts, func(name, state string) {
		if emit != nil {
			emit("progress", "configuration lifecycle action", map[string]any{"name": name, "state": state})
		}
	})
	if err != nil {
		for _, name := range suspended {
			_, _ = namespace.ReconcileDesired(context.Background(), engine.Paths, name)
		}
		return nil, mapError(err)
	}
	for _, change := range preview.Changes {
		if change.Kind == "delete" {
			_ = namespace.Forget(engine.Paths, change.Name)
			continue
		}
		if namespace.Desired(engine.Paths, change.Name) {
			_, _ = namespace.ReconcileDesired(context.Background(), engine.Paths, change.Name)
		}
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
	preview, previewErr := mounts.PreviewPrevious(engine.Paths)
	if previewErr != nil {
		return nil, mapError(previewErr)
	}
	suspended := make([]string, 0, len(preview.RequiresRestart))
	for _, name := range preview.RequiresRestart {
		if namespace.Desired(engine.Paths, name) {
			if _, suspendErr := namespace.SuspendOwned(ctx, engine.Paths, name); suspendErr != nil {
				return nil, mapError(suspendErr)
			}
			suspended = append(suspended, name)
		}
	}
	report, err := mounts.RollbackPrevious(ctx, engine.Paths, args.ExpectedRevision, args.PreviousDigest, func(name, state string) {
		if emit != nil {
			emit("progress", "configuration rollback lifecycle action", map[string]any{"name": name, "state": state})
		}
	})
	if err != nil {
		for _, name := range suspended {
			_, _ = namespace.ReconcileDesired(context.Background(), engine.Paths, name)
		}
		return nil, mapError(err)
	}
	for _, change := range preview.Changes {
		if change.Kind == "delete" {
			_ = namespace.Forget(engine.Paths, change.Name)
			continue
		}
		if namespace.Desired(engine.Paths, change.Name) {
			_, _ = namespace.ReconcileDesired(context.Background(), engine.Paths, change.Name)
		}
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

func mountHealth(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name,omitempty"`
		Boot bool   `json:"boot,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name != "" {
		health, err := supervisor.InspectOne(ctx, engine.Paths, args.Name, args.Boot)
		if err != nil {
			return nil, mapError(err)
		}
		return health, nil
	}
	health, err := supervisor.InspectAll(ctx, engine.Paths, args.Boot)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"health": health}, nil
}

func namespaceInspect(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	result, err := namespace.Inspect(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func namespacePreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	result, err := namespace.Preview(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func namespaceApply(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	if emit != nil {
		emit("progress", "applying namespace visibility", map[string]any{"name": args.Name})
	}
	result, err := namespace.Apply(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func namespaceRollbackPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	result, err := namespace.RollbackPreview(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func namespaceRollback(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	if emit != nil {
		emit("progress", "releasing namespace visibility", map[string]any{"name": args.Name})
	}
	result, err := namespace.Rollback(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func namespaceReconcile(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	names := []string{}
	if args.Name != "" {
		names = append(names, args.Name)
	} else {
		configured, err := mounts.List(engine.Paths)
		if err != nil {
			return nil, mapError(err)
		}
		for _, name := range configured {
			if namespace.Desired(engine.Paths, name) {
				names = append(names, name)
			}
		}
	}
	reports := make([]namespace.MutationReport, 0, len(names))
	failures := []map[string]string{}
	for _, name := range names {
		if emit != nil {
			emit("progress", "reconciling namespace visibility", map[string]any{"name": name})
		}
		report, err := namespace.ReconcileDesired(ctx, engine.Paths, name)
		if err != nil {
			mapped := mapError(err)
			failures = append(failures, map[string]string{"name": name, "code": mapped.Code, "message": mapped.Message})
			continue
		}
		reports = append(reports, report)
	}
	return map[string]any{"reports": reports, "failures": failures}, nil
}

func operationStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		RequestID string `json:"request_id"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.RequestID == "" {
		return nil, protocol.Error("invalid_argument", "request_id is required", "")
	}
	record, err := journal.Get(engine.Paths, args.RequestID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, protocol.Error("operation_not_found", "operation journal entry was not found", args.RequestID)
		}
		return nil, protocol.Error("operation_journal_unavailable", "operation journal could not be read", err.Error())
	}
	return record, nil
}

func operationList(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Limit int `json:"limit,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	records, err := journal.List(engine.Paths, args.Limit)
	if err != nil {
		return nil, protocol.Error("operation_journal_unavailable", "operation journal could not be listed", err.Error())
	}
	return map[string]any{"operations": records}, nil
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
	if namespace.Desired(engine.Paths, args.Name) {
		if _, visibilityErr := namespace.ReconcileDesired(context.Background(), engine.Paths, args.Name); visibilityErr != nil && emit != nil {
			mapped := mapError(visibilityErr)
			emit("visibility", "namespace visibility will be retried by the supervisor", map[string]any{"name": args.Name, "code": mapped.Code, "message": mapped.Message})
		}
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
	if namespace.Desired(engine.Paths, args.Name) {
		if _, suspendErr := namespace.SuspendOwned(ctx, engine.Paths, args.Name); suspendErr != nil {
			return nil, mapError(suspendErr)
		}
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
	if namespace.Desired(engine.Paths, args.Name) {
		if _, suspendErr := namespace.SuspendOwned(ctx, engine.Paths, args.Name); suspendErr != nil {
			return nil, mapError(suspendErr)
		}
	}
	result, err := mounts.Restart(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	if namespace.Desired(engine.Paths, args.Name) {
		if _, visibilityErr := namespace.ReconcileDesired(context.Background(), engine.Paths, args.Name); visibilityErr != nil && emit != nil {
			mapped := mapError(visibilityErr)
			emit("visibility", "namespace visibility will be retried by the supervisor", map[string]any{"name": args.Name, "code": mapped.Code, "message": mapped.Message})
		}
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
	found, cancellable, cancelled := engine.Cancel(args.RequestID)
	if !found {
		return nil, protocol.Error("operation_not_found", "active operation was not found", args.RequestID)
	}
	if !cancellable {
		return nil, protocol.Error("operation_not_cancellable", "operation does not support safe cancellation", args.RequestID)
	}
	return map[string]any{"request_id": args.RequestID, "cancelled": cancelled}, nil
}

func mountReconcile(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Boot bool `json:"boot,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	report, err := supervisor.Reconcile(ctx, engine.Paths, args.Boot, func(name, state string) {
		if emit != nil {
			emit("progress", "reconcile mount", map[string]any{"name": name, "state": state})
		}
	})
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"boot": args.Boot, "changed": report.Changed, "failures": report.Failures, "health": report.Health}, nil
}

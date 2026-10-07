package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"rclone-nexus/internal/buildinfo"
	cachegov "rclone-nexus/internal/cache"
	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/doctor"
	"rclone-nexus/internal/integrity"
	"rclone-nexus/internal/jobs"
	"rclone-nexus/internal/journal"
	"rclone-nexus/internal/migration"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/namespace"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/platformlifecycle"
	"rclone-nexus/internal/platformstate"
	"rclone-nexus/internal/policy"
	"rclone-nexus/internal/previewproof"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/rc"
	"rclone-nexus/internal/rootmgr"
	"rclone-nexus/internal/runtimeauth"
	"rclone-nexus/internal/supervisor"
	"rclone-nexus/internal/vfs"
	"rclone-nexus/internal/websettings"
)

type Emitter func(event, message string, data any)

type Handler func(context.Context, *Engine, json.RawMessage, Emitter) (any, *protocol.MachineError)

type operation struct {
	descriptor  protocol.OperationDescriptor
	handler     Handler
	cancellable bool
}

type requestIDContextKey struct{}

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
	engine.register("runtime.status", protocol.ClassQuery, "Inspect canonical bundled runtime/config authority", runtimeStatus)
	// Static-runtime builds intentionally expose no live runtime import,
	// source, activation, rollback, update, or Runtime Manager RPC surface.
	// Runtime replacement is a package/build concern: flash a module with a
	// different bundled system/bin/rclone.
	engine.register("migration.status", protocol.ClassQuery, "Inspect durable standalone migration authority state", migrationStatus)
	engine.register("migration.inspect", protocol.ClassQuery, "Inspect legacy NewFuture provider state without mutation", migrationInspect)
	engine.register("migration.preview", protocol.ClassPreview, "Preview provider config/mount/job import and issue a mutation proof", migrationPreview)
	engine.registerCancellable("migration.apply", protocol.ClassRun, "Import provider configuration and reviewed definitions disabled, then quiesce provider lifecycle", migrationApply)
	engine.register("migration.finalize.preview", protocol.ClassPreview, "Preview final standalone authority switch after explicit provider disable", migrationFinalizePreview)
	engine.registerCancellable("migration.finalize", protocol.ClassRun, "Enable reviewed Nexus definitions and finalize standalone authority", migrationFinalize)
	engine.registerCancellable("migration.rollback", protocol.ClassRun, "Restore the pre-migration Nexus state before standalone completion", migrationRollback)
	engine.registerCancellable("migration.recover", protocol.ClassReconcile, "Recover interrupted migration or fail closed on competing authority", migrationRecover)
	engine.register("provider.status", protocol.ClassQuery, "Inspect provider/rclone/FUSE/config readiness", providerStatus)
	engine.register("provider.remotes", protocol.ClassQuery, "List configured rclone remote names without credentials", providerRemotes)
	engine.register("provider.browse", protocol.ClassQuery, "Browse a configured remote path without exposing credentials", providerBrowse)
	engine.register("doctor.report", protocol.ClassQuery, "Run structured Nexus diagnostics", doctorReport)
	engine.register("doctor.bundle", protocol.ClassRun, "Create a deterministic sanitized support bundle", doctorBundle)
	engine.register("doctor.bundle.read", protocol.ClassQuery, "Read one Nexus-created bounded support bundle", doctorBundleRead)
	engine.register("diagnostics.logs", protocol.ClassQuery, "Read bounded sanitized structured Nexus logs", diagnosticsLogs)
	engine.register("platform.status", protocol.ClassQuery, "Inspect root-manager capabilities and module integrity", platformStatus)
	engine.register("config.snapshot", protocol.ClassQuery, "Read the credential-free configuration registry", configSnapshot)
	engine.register("config.validate", protocol.ClassQuery, "Validate a full candidate registry without issuing a mutation proof", configValidate)
	engine.register("config.preview", protocol.ClassPreview, "Validate a full candidate registry and preview its diff", configPreview)
	engine.register("config.apply", protocol.ClassRun, "Atomically publish a revision-bound candidate registry", configApply)
	engine.register("config.previous", protocol.ClassQuery, "Read previous-known-good configuration metadata", configPrevious)
	engine.register("config.rollback.preview", protocol.ClassPreview, "Preview rollback to previous-known-good configuration", configRollbackPreview)
	engine.register("config.rollback", protocol.ClassRun, "Rollback to previous-known-good configuration", configRollback)
	engine.register("mount.list", protocol.ClassQuery, "List configured mount names", mountList)
	engine.register("mount.status", protocol.ClassQuery, "Read one or all managed mount states", mountStatus)
	engine.register("mount.health", protocol.ClassQuery, "Read supervisor health/readiness state", mountHealth)
	engine.register("policy.status", protocol.ClassQuery, "Read resource-policy decisions without changing lifecycle state", policyStatus)
	engine.register("vfs.profiles", protocol.ClassQuery, "List named VFS profiles and advisory resource recommendation", vfsProfiles)
	engine.register("cache.status", protocol.ClassQuery, "Read owned VFS cache pressure and limits", cacheStatus)
	engine.register("cache.prune.preview", protocol.ClassPreview, "Preview bounded owned-cache pruning", cachePrunePreview)
	engine.registerCancellable("cache.prune", protocol.ClassRun, "Prune owned cache to configured low-water limits", cachePrune)
	engine.register("cache.clear.preview", protocol.ClassPreview, "Preview owned-cache file clearing", cacheClearPreview)
	engine.registerCancellable("cache.clear", protocol.ClassRun, "Clear regular files from a stopped mount's owned cache", cacheClear)
	engine.register("cache.forget.preview", protocol.ClassPreview, "Preview complete owned-cache reset", cacheForgetPreview)
	engine.registerCancellable("cache.forget", protocol.ClassRun, "Reset a stopped mount's owned cache tree", cacheForget)
	engine.register("namespace.inspect", protocol.ClassQuery, "Inspect Android mount namespace topology and observed visibility", namespaceInspect)
	engine.register("namespace.preview", protocol.ClassPreview, "Preview app-visibility namespace mutations", namespacePreview)
	engine.register("namespace.apply", protocol.ClassRun, "Apply app-visibility namespace mutations transactionally", namespaceApply)
	engine.register("namespace.rollback.preview", protocol.ClassPreview, "Preview release of Nexus-owned namespace binds", namespaceRollbackPreview)
	engine.register("namespace.rollback", protocol.ClassRun, "Release Nexus-owned namespace binds and disable app visibility", namespaceRollback)
	engine.register("namespace.reconcile", protocol.ClassReconcile, "Reconcile persisted app-visibility intent after namespace churn", namespaceReconcile)
	engine.register("ui.settings", protocol.ClassQuery, "Read persisted WebUI presentation settings", uiSettings)
	engine.register("ui.settings.preview", protocol.ClassPreview, "Validate and preview WebUI settings", uiSettingsPreview)
	engine.register("ui.settings.apply", protocol.ClassRun, "Persist preview-bound WebUI settings", uiSettingsApply)
	engine.register("jobs.snapshot", protocol.ClassQuery, "Read the typed scheduled-job registry", jobsSnapshot)
	engine.register("jobs.preview", protocol.ClassPreview, "Validate and preview the scheduled-job registry", jobsPreview)
	engine.register("jobs.apply", protocol.ClassRun, "Atomically publish the scheduled-job registry", jobsApply)
	engine.register("jobs.status", protocol.ClassQuery, "Read persisted scheduled-job execution state", jobsStatus)
	engine.register("job.run.preview", protocol.ClassPreview, "Preview a managed rclone job and its policy/destructive status", jobRunPreview)
	engine.registerCancellable("job.run", protocol.ClassRun, "Run one allow-listed managed rclone job", jobRun)
	engine.register("rc.metrics", protocol.ClassQuery, "Read allow-listed local-only rclone RC metrics", rcMetrics)
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

func (e *Engine) Descriptor(name string) (protocol.OperationDescriptor, bool) {
	op, ok := e.ops[name]
	if !ok {
		return protocol.OperationDescriptor{}, false
	}
	return op.descriptor, true
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
	operationContext := context.WithValue(ctx, requestIDContextKey{}, request.RequestID)
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
			operationContext, cancel = context.WithCancel(operationContext)
		} else {
			operationContext = context.WithoutCancel(operationContext)
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
	_ = diagnostics.Append(e.Paths, "operation", request.Operation.Name, "started", "", map[string]any{"class": request.Operation.Class})
	result, machineError := op.handler(operationContext, e, request.Operation.Args, wrappedEmit)
	if machineError != nil {
		_ = diagnostics.Append(e.Paths, "operation", request.Operation.Name, "failed", machineError.Code, map[string]any{"class": request.Operation.Class})
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
	_ = diagnostics.Append(e.Paths, "operation", request.Operation.Name, "succeeded", "", map[string]any{"class": request.Operation.Class})
	return protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: request.RequestID, Protocol: selected, OK: true, Result: result}
}

func requestIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDContextKey{}).(string); ok {
		return value
	}
	return ""
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

func actionPreviewBinding(engine *Engine, resource, name string) (uint64, string, *protocol.MachineError) {
	registry, err := mounts.LoadRegistry(engine.Paths)
	if err != nil {
		return 0, "", mapError(err)
	}
	found := false
	for _, cfg := range registry.Mounts {
		if cfg.Name == name {
			found = true
			break
		}
	}
	if !found {
		return 0, "", protocol.Error("mount_not_found", "mount is not configured", name)
	}
	sum := sha256.Sum256([]byte(resource + "\x00" + name + "\x00" + registry.Digest))
	return registry.Revision, hex.EncodeToString(sum[:]), nil
}

func proofedActionPreview(engine *Engine, resource, name string, value any) (any, *protocol.MachineError) {
	revision, digest, machineErr := actionPreviewBinding(engine, resource, name)
	if machineErr != nil {
		return nil, machineErr
	}
	proof, err := previewproof.Issue(engine.Paths, resource, revision, digest)
	if err != nil {
		return nil, mapError(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, mapError(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, mapError(err)
	}
	out["current_revision"] = revision
	out["candidate_digest"] = digest
	out["preview_proof"] = proof.Token
	out["preview_expires_unix_ms"] = proof.ExpiresUnixMS
	return out, nil
}

func consumeActionPreview(engine *Engine, resource, name string, expectedRevision uint64, candidateDigest, token string) *protocol.MachineError {
	if err := previewproof.Consume(engine.Paths, token, resource, expectedRevision, candidateDigest); err != nil {
		return mapError(err)
	}
	currentRevision, currentDigest, machineErr := actionPreviewBinding(engine, resource, name)
	if machineErr != nil {
		return machineErr
	}
	if currentRevision != expectedRevision {
		return protocol.Error("stale_revision", "configuration revision is stale", fmt.Sprintf("expected=%d actual=%d", expectedRevision, currentRevision))
	}
	if currentDigest != candidateDigest {
		return protocol.Error("preview_mismatch", "preview no longer matches the current mount configuration", "preview again")
	}
	return nil
}

func structuredMountError(code, category, field, message, detail, suggestion string) *protocol.MachineError {
	return protocol.ErrorWithIssues(code, category, message, detail, true, []protocol.ValidationIssue{{
		Code: code, Category: category, Field: field, Severity: "error", Message: message, Detail: detail, Suggestion: suggestion,
	}})
}

func inferMountValidationError(message string) *protocol.MachineError {
	switch {
	case strings.Contains(message, "mountpoints overlap"):
		return structuredMountError("mountpoint_overlap", "destination", "mountpoint", "Mount location overlaps another mount", message, "Choose a separate mount location.")
	case strings.Contains(message, "mountpoint must be absolute"):
		return structuredMountError("mountpoint_absolute_required", "destination", "mountpoint", "Mount location must be an absolute path", message, "/storage/emulated/0/Rclone/<name>")
	case strings.Contains(message, "mountpoint cannot be filesystem root"):
		return structuredMountError("mountpoint_root_forbidden", "destination", "mountpoint", "Filesystem root cannot be used as a mount location", message, "Choose a folder under shared storage.")
	case strings.Contains(message, "mountpoint overlaps protected"):
		return structuredMountError("mountpoint_protected", "security", "mountpoint", "Mount location overlaps Nexus or provider state", message, "Choose a folder under shared storage.")
	case strings.Contains(message, "missing remote"):
		return structuredMountError("remote_required", "remote", "remote", "Choose an rclone remote", message, "Select a configured remote and folder.")
	case strings.Contains(message, "remote contains control"):
		return structuredMountError("invalid_remote", "remote", "remote", "Remote contains unsupported characters", message, "Choose the remote again or enter a clean remote:path value.")
	case strings.Contains(message, "invalid mount name"):
		return structuredMountError("invalid_mount_name", "input", "name", "Mount name is not valid", message, "Use letters, numbers, dot, underscore, or dash.")
	case strings.Contains(message, "vfs_cache_max_size"):
		return structuredMountError("invalid_cache_size", "vfs", "vfs_cache_max_size", "Cache size is not valid", message, "Use a value such as 512MiB, 2GiB, or 8GiB.")
	case strings.Contains(message, "vfs_cache_max_age"):
		return structuredMountError("invalid_duration", "vfs", "vfs_cache_max_age", "Cache maximum age is not valid", message, "Use values such as 30m, 24h, or 7d.")
	case strings.Contains(message, "dir_cache_time"):
		return structuredMountError("invalid_duration", "vfs", "dir_cache_time", "Directory cache duration is not valid", message, "Use values such as 5m, 30m, or 24h.")
	case strings.Contains(message, "poll_interval"):
		return structuredMountError("invalid_duration", "vfs", "poll_interval", "Poll interval is not valid", message, "Use values such as 15s, 1m, or 5m.")
	case strings.Contains(message, "vfs_profile"):
		return structuredMountError("invalid_vfs_profile", "vfs", "vfs_profile", "Performance profile is not supported", message, "Choose one of the profiles offered by Nexus.")
	case strings.Contains(message, "vfs_cache_mode"):
		return structuredMountError("invalid_vfs_cache_mode", "vfs", "vfs_cache_mode", "VFS cache mode is not supported", message, "Choose off, minimal, writes, or full.")
	case strings.Contains(message, "network_mode"):
		return structuredMountError("invalid_network_policy", "policy", "network_mode", "Network policy is not supported", message, "Choose a supported network policy.")
	case strings.Contains(message, "min_battery"):
		return structuredMountError("invalid_min_battery", "policy", "min_battery", "Minimum battery must be between 0 and 100", message, "Use 0 to disable the battery threshold.")
	case strings.Contains(message, "min_free_cache_space"):
		return structuredMountError("invalid_min_cache_free", "vfs", "min_free_cache_space", "Minimum free cache space is not valid", message, "Use a value such as 512MiB or 2GiB.")
	case strings.Contains(message, "cache water marks"):
		return structuredMountError("invalid_cache_watermarks", "vfs", "cache_low_water", "Cache watermarks are inconsistent", message, "Keep low-water below high-water, for example 75% and 90%.")
	case strings.Contains(message, "boot_settle"):
		return structuredMountError("invalid_duration", "advanced", "boot_settle", "Boot settle delay is not valid", message, "Use values such as 5s or 30s.")
	case strings.Contains(message, "network_settle"):
		return structuredMountError("invalid_duration", "advanced", "network_settle", "Network settle delay is not valid", message, "Use values such as 5s or 30s.")
	case strings.Contains(message, "unsupported log_level"):
		return structuredMountError("invalid_log_level", "advanced", "log_level", "Log level is not supported", message, "Choose DEBUG, INFO, NOTICE, or ERROR.")
	}
	return nil
}

func lifecycleSuggestion(code string) string {
	switch code {
	case "rclone_cli_incompatible":
		return "Update Nexus/provider compatibility or remove the unsupported rclone option before retrying."
	case "remote_auth_error":
		return "Refresh the remote credentials, then retry the mount."
	case "remote_offline":
		return "Check connectivity or wait for the remote to return; this failure is retryable."
	case "fuse_start_failed":
		return "Check FUSE availability and the rooted mount/unmount environment; an external provider helper is optional in managed mode."
	case "runtime_migration_required":
		return "Migrate the legacy provider runtime into Nexus managed runtime, or explicitly select external compatibility mode."
	case "runtime_authority_ambiguous":
		return "Disable the competing legacy provider lifecycle authority before using managed mode."
	case "runtime_authority_unavailable":
		return "Restore the runtime selected by the canonical Nexus runtime authority before starting this mount."
	case "runtime_config_unavailable":
		return "Restore the configuration selected by the canonical Nexus runtime authority before starting this mount."
	case "mount_config_invalid", "mount_args_invalid":
		return "Edit the mount configuration and review it again before starting."
	case "provider_cli_probe_failed":
		return "Repair provider execution so Nexus can qualify the installed rclone CLI before starting."
	default:
		return "Open Logs for the bounded startup diagnostic before retrying."
	}
}

func mapError(err error) *protocol.MachineError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return protocol.Error("operation_cancelled", "operation was cancelled", "")
	}
	var lifecycleErr *mounts.LifecycleError
	if errors.As(err, &lifecycleErr) {
		return &protocol.MachineError{
			Code: lifecycleErr.Code, Message: lifecycleErr.Message, Detail: lifecycleErr.Detail,
			Category: lifecycleErr.Category, Stage: lifecycleErr.Stage, Severity: "error", Retryable: lifecycleErr.Retryable, ExitCode: lifecycleErr.ExitCode,
			Issues: []protocol.ValidationIssue{{Code: lifecycleErr.Code, Category: lifecycleErr.Category, Severity: "error", Message: lifecycleErr.Message, Detail: lifecycleErr.Detail, Suggestion: lifecycleSuggestion(lifecycleErr.Code)}},
		}
	}
	message := err.Error()
	if machineErr := inferMountValidationError(message); machineErr != nil {
		return machineErr
	}
	switch {
	case mounts.IsStaleRevision(err):
		return protocol.Error("stale_revision", "configuration revision is stale", message)
	case mounts.IsCandidateDigestMismatch(err):
		return protocol.Error("candidate_digest_mismatch", "candidate digest does not match preview", message)
	case mounts.IsPreviousUnavailable(err):
		return protocol.Error("previous_config_unavailable", "previous-known-good configuration is unavailable", message)
	case previewproof.Code(err) == "preview_required":
		return protocol.Error("preview_required", "a fresh configuration preview is required", message)
	case previewproof.Code(err) == "preview_expired":
		return protocol.Error("preview_expired", "configuration preview expired; preview again", message)
	case previewproof.Code(err) == "preview_mismatch":
		return protocol.Error("preview_mismatch", "configuration preview does not match this apply request", message)
	case previewproof.Code(err) == "preview_invalid":
		return protocol.Error("preview_invalid", "configuration preview proof is invalid", message)
	case strings.Contains(message, "cache path escapes") || strings.Contains(message, "owned cache root is a symlink") || strings.Contains(message, "cache deletion escaped") || strings.Contains(message, "refusing non-regular cache deletion"):
		return protocol.Error("cache_ownership_invalid", "cache ownership proof failed", message)
	case strings.Contains(message, "namespace strategy unsupported"):
		return protocol.Error("namespace_strategy_unsupported", "no qualified namespace visibility strategy is available", message)
	case strings.Contains(message, "namespace ownership mismatch"):
		return protocol.Error("namespace_ownership_mismatch", "namespace mount no longer matches Nexus ownership evidence", message)
	case strings.Contains(message, "target mountpoint unavailable") || strings.Contains(message, "mount namespace"):
		return protocol.Error("namespace_target_unavailable", "target mount namespace is unavailable", message)
	case strings.Contains(message, "stale job revision"):
		return protocol.Error("stale_revision", "job configuration revision is stale", message)
	case strings.Contains(message, "job candidate digest mismatch"):
		return protocol.Error("candidate_digest_mismatch", "job candidate digest does not match preview", message)
	case strings.Contains(message, "WebUI settings") || strings.Contains(message, "refresh_seconds") || strings.Contains(message, "log_limit") || strings.Contains(message, "default_view"):
		return protocol.Error("invalid_ui_settings", "WebUI settings are invalid", message)
	case strings.Contains(message, "invalid remote path") || strings.Contains(message, "remote is not configured"):
		return protocol.ErrorWithIssues("invalid_remote_browse", "remote", "Remote browse request is invalid", message, true, []protocol.ValidationIssue{{Code: "invalid_remote_browse", Category: "remote", Field: "remote", Severity: "error", Message: "Remote path could not be browsed", Detail: message, Suggestion: "Choose a configured remote and folder."}})
	case strings.Contains(message, "rclone query failed"):
		return protocol.ErrorWithIssues("remote_query_failed", "remote", "Remote could not be reached", message, true, []protocol.ValidationIssue{{Code: "remote_query_failed", Category: "remote", Field: "remote", Severity: "error", Message: "Remote could not be reached", Detail: message, Suggestion: "Check connectivity or authentication, then retry."}})
	case strings.Contains(message, "job policy blocked"):
		return protocol.Error("policy_blocked", "job resource policy blocks execution", message)
	case strings.Contains(message, "job already running"):
		return protocol.Error("job_already_running", "job is already running", "")
	case strings.Contains(message, "job not found"):
		return protocol.Error("job_not_found", "job not found", "")
	case strings.Contains(message, "invalid job") || strings.Contains(message, "unsupported job") || strings.Contains(message, "sync job requires") || strings.Contains(message, "local job endpoint") || strings.Contains(message, "invalid remote name"):
		return protocol.Error("invalid_job_config", "job configuration is invalid", message)
	case strings.Contains(message, "rc endpoint is not loopback"):
		return protocol.Error("rc_endpoint_invalid", "RC endpoint is not local-only", "")
	case strings.Contains(message, "mount definition not found"):
		return protocol.Error("mount_not_found", "mount definition not found", message)
	case strings.Contains(message, "runtime migration required"):
		return protocol.Error("runtime_migration_required", "runtime migration is required", message)
	case strings.Contains(message, "runtime authority ambiguous"):
		return protocol.Error("runtime_authority_ambiguous", "runtime authority is ambiguous", message)
	case strings.Contains(message, "managed rclone runtime not found") || strings.Contains(message, "external runtime mode requires") || strings.Contains(message, "rclone binary not found"):
		return protocol.Error("runtime_authority_unavailable", "canonical rclone runtime is unavailable", message)
	case strings.Contains(message, "rclone config not found") || strings.Contains(message, "config path is not configured"):
		return protocol.Error("runtime_config_unavailable", "canonical rclone configuration is unavailable", message)
	default:
		return protocol.Error("operation_failed", "operation failed", message)
	}
}

func doctorReport(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if machineErr := strictArgs(raw, &args); machineErr != nil {
		return nil, machineErr
	}
	return doctor.Run(ctx, engine.Paths), nil
}

func doctorBundle(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if machineErr := strictArgs(raw, &args); machineErr != nil {
		return nil, machineErr
	}
	if emit != nil {
		emit("progress", "building sanitized support bundle", map[string]any{"phase": "collect"})
	}
	result, err := doctor.BuildBundle(ctx, engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func platformStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if machineErr := strictArgs(raw, &args); machineErr != nil {
		return nil, machineErr
	}
	state := map[string]any{"current_schema": platformstate.CurrentSchema}
	if current, err := platformstate.Read(engine.Paths); err == nil {
		state["schema_version"] = current.SchemaVersion
	} else if os.IsNotExist(err) {
		state["schema_version"] = 0
	} else {
		state["error"] = "state_unreadable"
	}
	return map[string]any{"root_manager": rootmgr.Detect(), "integrity": integrity.Verify(engine.Paths.ModuleDir), "state": state, "purge_on_uninstall": platformlifecycle.PurgeState(engine.Paths)}, nil
}

func runtimeStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	status, err := runtimeauth.Resolve(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return status, nil
}

func migrationStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	s, ok, err := migration.StateSnapshot(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"present": ok, "state": s}, nil
}

func migrationInspect(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	d, err := migration.Detect(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return d, nil
}

func migrationPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var req migration.PreviewRequest
	if err := strictArgs(raw, &req); err != nil {
		return nil, err
	}
	v, err := migration.PreviewMigration(engine.Paths, req)
	if err != nil {
		return nil, mapError(err)
	}
	proof, err := previewproof.Issue(engine.Paths, "migration", v.Generation, v.CandidateDigest)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"preview": v, "current_revision": v.Generation, "candidate_digest": v.CandidateDigest, "preview_proof": proof.Token, "preview_expires_unix_ms": proof.ExpiresUnixMS}, nil
}

func migrationApply(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Request          migration.PreviewRequest `json:"request"`
		ExpectedRevision uint64                   `json:"expected_revision"`
		CandidateDigest  string                   `json:"candidate_digest"`
		PreviewProof     string                   `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	v, err := migration.PreviewMigration(engine.Paths, args.Request)
	if err != nil {
		return nil, mapError(err)
	}
	if !v.CanApply {
		return nil, protocol.Error("migration_conflict", "migration preview contains conflicts", strings.Join(v.Conflicts, "; "))
	}
	if v.Generation != args.ExpectedRevision {
		return nil, protocol.Error("stale_revision", "migration state generation is stale", fmt.Sprintf("expected=%d actual=%d", args.ExpectedRevision, v.Generation))
	}
	if v.CandidateDigest != args.CandidateDigest {
		return nil, protocol.Error("preview_mismatch", "migration preview no longer matches", "preview again")
	}
	if err := previewproof.Consume(engine.Paths, args.PreviewProof, "migration", args.ExpectedRevision, args.CandidateDigest); err != nil {
		return nil, mapError(err)
	}
	emit("progress", "quiescing legacy provider and importing reviewed state disabled", map[string]any{"phase": "migration_apply"})
	state, err := migration.Apply(ctx, engine.Paths, v)
	if err != nil {
		return state, mapError(err)
	}
	return state, nil
}

func migrationFinalizePreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	v, err := migration.PreviewFinalize(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	proof, err := previewproof.Issue(engine.Paths, "migration-finalize", v.Generation, v.CandidateDigest)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"preview": v, "current_revision": v.Generation, "candidate_digest": v.CandidateDigest, "preview_proof": proof.Token, "preview_expires_unix_ms": proof.ExpiresUnixMS}, nil
}

func migrationFinalize(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64 `json:"expected_revision"`
		CandidateDigest  string `json:"candidate_digest"`
		PreviewProof     string `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	v, err := migration.PreviewFinalize(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	if !v.CanFinalize {
		return nil, protocol.Error("migration_provider_active", "standalone authority cannot finalize while provider lifecycle is active", strings.Join(v.Conflicts, "; "))
	}
	if v.Generation != args.ExpectedRevision || v.CandidateDigest != args.CandidateDigest {
		return nil, protocol.Error("preview_mismatch", "migration finalization preview is stale", "preview again")
	}
	if err := previewproof.Consume(engine.Paths, args.PreviewProof, "migration-finalize", args.ExpectedRevision, args.CandidateDigest); err != nil {
		return nil, mapError(err)
	}
	emit("progress", "enabling reviewed Nexus definitions and finalizing standalone authority", map[string]any{"phase": "migration_finalize"})
	state, err := migration.Finalize(ctx, engine.Paths)
	if err != nil {
		return state, mapError(err)
	}
	return state, nil
}

func migrationRollback(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	state, err := migration.Rollback(ctx, engine.Paths)
	if err != nil {
		return state, mapError(err)
	}
	return state, nil
}

func migrationRecover(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	state, present, err := migration.Recover(ctx, engine.Paths)
	if err != nil {
		return map[string]any{"present": present, "state": state}, mapError(err)
	}
	return map[string]any{"present": present, "state": state}, nil
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

func protocolIssues(items []mounts.ValidationIssue) []protocol.ValidationIssue {
	out := make([]protocol.ValidationIssue, 0, len(items))
	for _, item := range items {
		out = append(out, protocol.ValidationIssue{
			Code: item.Code, Category: item.Category, Mount: item.Mount, Field: item.Field, Severity: item.Severity,
			Message: item.Message, Detail: item.Detail, Suggestion: item.Suggestion, RelatedMount: item.RelatedMount,
		})
	}
	return out
}

func remoteName(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	name, _, ok := strings.Cut(endpoint, ":")
	if !ok || name == "" || strings.ContainsAny(name, "/\\") {
		return ""
	}
	return name
}

func configValidate(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Mounts []mounts.CandidateConfig `json:"mounts"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	report, err := mounts.ValidateCandidate(engine.Paths, args.Mounts)
	if err != nil {
		return nil, mapError(err)
	}
	issues := protocolIssues(report.Issues)
	configured, remoteErr := provider.ListRemotes(ctx, engine.Paths)
	configuredSet := map[string]bool{}
	for _, name := range configured {
		configuredSet[name] = true
	}
	if remoteErr != nil {
		issues = append(issues, protocol.ValidationIssue{
			Code: "provider_remotes_unavailable", Category: "provider", Severity: "error",
			Message: "Configured remotes could not be read", Detail: remoteErr.Error(),
			Suggestion: "Check the canonical runtime configuration and retry.",
		})
	} else {
		for _, cfg := range args.Mounts {
			if cfg.Remote == "" {
				continue
			}
			name := remoteName(cfg.Remote)
			if name == "" {
				issues = append(issues, protocol.ValidationIssue{
					Code: "remote_format_unverified", Category: "remote", Mount: cfg.Name, Field: "remote", Severity: "warning",
					Message: "Remote could not be matched to a configured rclone remote",
					Detail:  cfg.Remote, Suggestion: "Choose a configured remote from the browser or verify the manual remote:path value.",
				})
				continue
			}
			if !configuredSet[name] {
				issues = append(issues, protocol.ValidationIssue{
					Code: "remote_not_configured", Category: "remote", Mount: cfg.Name, Field: "remote", Severity: "error",
					Message: "Remote is not configured", Detail: name + ":",
					Suggestion: "Choose one of the configured remotes or configure it in the provider first.",
				})
			}
		}
	}
	valid := true
	for _, issue := range issues {
		if issue.Severity == "error" {
			valid = false
			break
		}
	}
	return map[string]any{"valid": valid, "current_revision": report.CurrentRevision, "issues": issues}, nil
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
	proof, err := previewproof.Issue(engine.Paths, "config", preview.CurrentRevision, preview.CandidateDigest)
	if err != nil {
		return nil, mapError(err)
	}
	preview.PreviewProof = proof.Token
	preview.PreviewExpiresMS = proof.ExpiresUnixMS
	return preview, nil
}

func configApply(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64                   `json:"expected_revision"`
		CandidateDigest  string                   `json:"candidate_digest"`
		PreviewProof     string                   `json:"preview_proof"`
		Mounts           []mounts.CandidateConfig `json:"mounts"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	preview, previewErr := mounts.PreviewCandidate(engine.Paths, args.Mounts)
	if previewErr != nil {
		return nil, mapError(previewErr)
	}
	if err := previewproof.Consume(engine.Paths, args.PreviewProof, "config", args.ExpectedRevision, args.CandidateDigest); err != nil {
		return nil, mapError(err)
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
	proof, err := previewproof.Issue(engine.Paths, "config-rollback", preview.CurrentRevision, preview.CandidateDigest)
	if err != nil {
		return nil, mapError(err)
	}
	preview.PreviewProof = proof.Token
	preview.PreviewExpiresMS = proof.ExpiresUnixMS
	return preview, nil
}

func configRollback(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64 `json:"expected_revision"`
		PreviousDigest   string `json:"previous_digest"`
		PreviewProof     string `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	preview, previewErr := mounts.PreviewPrevious(engine.Paths)
	if previewErr != nil {
		return nil, mapError(previewErr)
	}
	if err := previewproof.Consume(engine.Paths, args.PreviewProof, "config-rollback", args.ExpectedRevision, args.PreviousDigest); err != nil {
		return nil, mapError(err)
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

func policyStatus(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name,omitempty"`
		Boot bool   `json:"boot,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	inspect := func(name string) (map[string]any, error) {
		cfg, err := mounts.Parse(engine.Paths, name)
		if err != nil {
			return nil, err
		}
		spec, err := mounts.PolicySpec(cfg)
		if err != nil {
			return nil, err
		}
		decision, err := policy.Evaluate(ctx, engine.Paths, name, spec, args.Boot)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": name, "network_mode": cfg.NetworkMode, "charging_only": cfg.ChargingOnly, "min_battery": cfg.MinBattery, "min_free_cache_space": cfg.MinFreeCacheSpace, "boot_settle": cfg.BootSettle, "network_settle": cfg.NetworkSettle, "decision": decision}, nil
	}
	if args.Name != "" {
		value, err := inspect(args.Name)
		if err != nil {
			return nil, mapError(err)
		}
		return value, nil
	}
	names, err := mounts.List(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		value, inspectErr := inspect(name)
		if inspectErr != nil {
			return nil, mapError(inspectErr)
		}
		out = append(out, value)
	}
	return map[string]any{"policies": out}, nil
}

func vfsProfiles(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	result := map[string]any{"profiles": vfs.Profiles(), "recommendation": vfs.Recommend(engine.Paths.Normalize().CacheDir)}
	if args.Name != "" {
		cfg, err := mounts.Parse(engine.Paths, args.Name)
		if err != nil {
			return nil, mapError(err)
		}
		effective, err := mounts.EffectiveVFS(cfg)
		if err != nil {
			return nil, mapError(err)
		}
		result["mount"] = map[string]any{"name": args.Name, "profile": cfg.VFSProfile, "effective": effective}
	}
	return result, nil
}

func cacheStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	inspect := func(name string) (cachegov.Status, error) {
		cfg, err := mounts.Parse(engine.Paths, name)
		if err != nil {
			return cachegov.Status{}, err
		}
		limits, err := mounts.CacheLimits(cfg)
		if err != nil {
			return cachegov.Status{}, err
		}
		return cachegov.Inspect(engine.Paths, name, limits)
	}
	if args.Name != "" {
		value, err := inspect(args.Name)
		if err != nil {
			return nil, mapError(err)
		}
		return value, nil
	}
	names, err := mounts.List(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]cachegov.Status, 0, len(names))
	for _, name := range names {
		value, inspectErr := inspect(name)
		if inspectErr != nil {
			return nil, mapError(inspectErr)
		}
		out = append(out, value)
	}
	return map[string]any{"caches": out}, nil
}

func cacheMutationInputs(engine *Engine, name string) (cachegov.Limits, *protocol.MachineError) {
	if name == "" {
		return cachegov.Limits{}, protocol.Error("invalid_argument", "name is required", "")
	}
	cfg, err := mounts.Parse(engine.Paths, name)
	if err != nil {
		return cachegov.Limits{}, mapError(err)
	}
	limits, err := mounts.CacheLimits(cfg)
	if err != nil {
		return cachegov.Limits{}, mapError(err)
	}
	return limits, nil
}

func requireCacheStopped(engine *Engine, name string) *protocol.MachineError {
	obs, err := mounts.ObserveRuntime(engine.Paths, name)
	if err != nil {
		return mapError(err)
	}
	if obs.ProcessAlive || obs.MountAlive {
		return protocol.Error("cache_mount_running", "cache mutation requires the mount to be stopped", name)
	}
	return nil
}

func cachePrunePreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	limits, merr := cacheMutationInputs(engine, args.Name)
	if merr != nil {
		return nil, merr
	}
	value, err := cachegov.PrunePreview(engine.Paths, args.Name, limits)
	if err != nil {
		return nil, mapError(err)
	}
	value.RequiresStopped = true
	return value, nil
}
func cacheClearPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if _, merr := cacheMutationInputs(engine, args.Name); merr != nil {
		return nil, merr
	}
	value, err := cachegov.ClearPreview(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	value.RequiresStopped = true
	return proofedActionPreview(engine, "cache-clear", args.Name, value)
}
func cacheForgetPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if _, merr := cacheMutationInputs(engine, args.Name); merr != nil {
		return nil, merr
	}
	value, err := cachegov.ForgetPreview(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return proofedActionPreview(engine, "cache-forget", args.Name, value)
}
func cachePrune(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	limits, merr := cacheMutationInputs(engine, args.Name)
	if merr != nil {
		return nil, merr
	}
	if merr = requireCacheStopped(engine, args.Name); merr != nil {
		return nil, merr
	}
	if emit != nil {
		emit("progress", "pruning owned cache", map[string]any{"name": args.Name})
	}
	value, err := cachegov.Prune(ctx, engine.Paths, args.Name, limits)
	if err != nil {
		return nil, mapError(err)
	}
	return value, nil
}
func cacheClear(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name             string `json:"name"`
		ExpectedRevision uint64 `json:"expected_revision"`
		CandidateDigest  string `json:"candidate_digest"`
		PreviewProof     string `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if machineErr := consumeActionPreview(engine, "cache-clear", args.Name, args.ExpectedRevision, args.CandidateDigest, args.PreviewProof); machineErr != nil {
		return nil, machineErr
	}
	if _, merr := cacheMutationInputs(engine, args.Name); merr != nil {
		return nil, merr
	}
	if merr := requireCacheStopped(engine, args.Name); merr != nil {
		return nil, merr
	}
	if emit != nil {
		emit("progress", "clearing owned cache", map[string]any{"name": args.Name})
	}
	value, err := cachegov.Clear(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	value.RequiresStopped = true
	return value, nil
}
func cacheForget(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name             string `json:"name"`
		ExpectedRevision uint64 `json:"expected_revision"`
		CandidateDigest  string `json:"candidate_digest"`
		PreviewProof     string `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if machineErr := consumeActionPreview(engine, "cache-forget", args.Name, args.ExpectedRevision, args.CandidateDigest, args.PreviewProof); machineErr != nil {
		return nil, machineErr
	}
	if _, merr := cacheMutationInputs(engine, args.Name); merr != nil {
		return nil, merr
	}
	if merr := requireCacheStopped(engine, args.Name); merr != nil {
		return nil, merr
	}
	if emit != nil {
		emit("progress", "resetting owned cache", map[string]any{"name": args.Name})
	}
	value, err := cachegov.Forget(ctx, engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return value, nil
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
	return proofedActionPreview(engine, "namespace-apply", args.Name, result)
}

func namespaceApply(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name             string `json:"name"`
		ExpectedRevision uint64 `json:"expected_revision"`
		CandidateDigest  string `json:"candidate_digest"`
		PreviewProof     string `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	if machineErr := consumeActionPreview(engine, "namespace-apply", args.Name, args.ExpectedRevision, args.CandidateDigest, args.PreviewProof); machineErr != nil {
		return nil, machineErr
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
	return proofedActionPreview(engine, "namespace-rollback", args.Name, result)
}

func namespaceRollback(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name             string `json:"name"`
		ExpectedRevision uint64 `json:"expected_revision"`
		CandidateDigest  string `json:"candidate_digest"`
		PreviewProof     string `json:"preview_proof"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	if machineErr := consumeActionPreview(engine, "namespace-rollback", args.Name, args.ExpectedRevision, args.CandidateDigest, args.PreviewProof); machineErr != nil {
		return nil, machineErr
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

func jobsSnapshot(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	r, err := jobs.Snapshot(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return r, nil
}
func jobsPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Jobs []jobs.Config `json:"jobs"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	v, err := jobs.PreviewCandidate(engine.Paths, args.Jobs)
	if err != nil {
		return nil, mapError(err)
	}
	proof, err := previewproof.Issue(engine.Paths, "jobs", v.CurrentRevision, v.CandidateDigest)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"current_revision": v.CurrentRevision, "candidate_digest": v.CandidateDigest, "jobs": v.Jobs, "destructive_sync_jobs": v.Destructive, "preview_proof": proof.Token, "preview_expires_ms": proof.ExpiresUnixMS}, nil
}
func jobsApply(_ context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64        `json:"expected_revision"`
		CandidateDigest  string        `json:"candidate_digest"`
		PreviewProof     string        `json:"preview_proof"`
		Jobs             []jobs.Config `json:"jobs"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := previewproof.Consume(engine.Paths, args.PreviewProof, "jobs", args.ExpectedRevision, args.CandidateDigest); err != nil {
		return nil, mapError(err)
	}
	if emit != nil {
		emit("progress", "publishing scheduled-job registry", map[string]any{"count": len(args.Jobs)})
	}
	r, err := jobs.ApplyCandidate(engine.Paths, args.ExpectedRevision, args.CandidateDigest, args.Jobs)
	if err != nil {
		return nil, mapError(err)
	}
	return r, nil
}
func jobsStatus(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	states, err := jobs.States(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"jobs": states}, nil
}
func jobRunPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	v, err := jobs.PreviewRun(engine.Paths, args.Name)
	if err != nil {
		return nil, mapError(err)
	}
	return v, nil
}
func jobRun(ctx context.Context, engine *Engine, raw json.RawMessage, emit Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name    string `json:"name"`
		Trigger string `json:"trigger,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Trigger == "" {
		args.Trigger = "manual"
	}
	result, err := jobs.Run(ctx, engine.Paths, args.Name, args.Trigger, requestIDFromContext(ctx), func(event, message string, data any) {
		if emit != nil {
			emit(event, message, data)
		}
	})
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}
func rcMetrics(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, protocol.Error("invalid_argument", "name is required", "")
	}
	v, err := rc.MetricsFor(ctx, engine.Paths, args.Name)
	if err != nil {
		return v, nil
	}
	return v, nil
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

func doctorBundleRead(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		BundleID string `json:"bundle_id"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	meta, data, err := doctor.BundleBytes(engine.Paths, args.BundleID)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"bundle_id": meta.BundleID, "filename": meta.Filename, "size": meta.Size, "sha256": meta.SHA256, "encoding": "base64", "data": base64.StdEncoding.EncodeToString(data)}, nil
}

func diagnosticsLogs(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Limit       int   `json:"limit,omitempty"`
		AfterUnixMS int64 `json:"after_unix_ms,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	logs, err := diagnostics.ReadLogs(engine.Paths, args.Limit, args.AfterUnixMS)
	if err != nil {
		return nil, mapError(err)
	}
	return logs, nil
}

func providerRemotes(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	names, err := provider.ListRemotes(ctx, engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"remotes": names}, nil
}
func providerBrowse(ctx context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Remote string `json:"remote"`
		Path   string `json:"path,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	result, err := provider.Browse(ctx, engine.Paths, args.Remote, args.Path, args.Limit)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func uiSettings(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct{}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	snap, err := websettings.Load(engine.Paths)
	if err != nil {
		return nil, mapError(err)
	}
	return snap, nil
}
func uiSettingsPreview(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		Settings websettings.Settings `json:"settings"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	p, err := websettings.PreviewCandidate(engine.Paths, args.Settings)
	if err != nil {
		return nil, mapError(err)
	}
	proof, err := previewproof.Issue(engine.Paths, "ui-settings", p.CurrentRevision, p.CandidateDigest)
	if err != nil {
		return nil, mapError(err)
	}
	return map[string]any{"current_revision": p.CurrentRevision, "candidate_digest": p.CandidateDigest, "settings": p.Settings, "preview_proof": proof.Token, "preview_expires_ms": proof.ExpiresUnixMS}, nil
}
func uiSettingsApply(_ context.Context, engine *Engine, raw json.RawMessage, _ Emitter) (any, *protocol.MachineError) {
	var args struct {
		ExpectedRevision uint64               `json:"expected_revision"`
		CandidateDigest  string               `json:"candidate_digest"`
		PreviewProof     string               `json:"preview_proof"`
		Settings         websettings.Settings `json:"settings"`
	}
	if err := strictArgs(raw, &args); err != nil {
		return nil, err
	}
	if err := previewproof.Consume(engine.Paths, args.PreviewProof, "ui-settings", args.ExpectedRevision, args.CandidateDigest); err != nil {
		return nil, mapError(err)
	}
	snap, err := websettings.Apply(engine.Paths, args.ExpectedRevision, args.CandidateDigest, args.Settings)
	if err != nil {
		return nil, mapError(err)
	}
	return snap, nil
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/buildinfo"
	"rclone-nexus/internal/control"
	"rclone-nexus/internal/daemon"
	"rclone-nexus/internal/doctor"
	"rclone-nexus/internal/integrity"
	"rclone-nexus/internal/journal"
	"rclone-nexus/internal/migration"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/platformlifecycle"
	"rclone-nexus/internal/platformstate"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/rootmgr"
	"rclone-nexus/internal/runtimeacquire"
	"rclone-nexus/internal/runtimeauth"
	"rclone-nexus/internal/runtimebuild"
	"rclone-nexus/internal/runtimesource"
	"rclone-nexus/internal/runtimestore"
	"rclone-nexus/internal/runtimeupdate"
	"rclone-nexus/internal/supervisor"
	"rclone-nexus/internal/webui"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var cliErr *exitError
		if errors.As(err, &cliErr) {
			if !cliErr.silent && cliErr.message != "" {
				fmt.Fprintln(os.Stderr, cliErr.message)
			}
			os.Exit(cliErr.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	p := paths.FromEnv()
	engine := control.New(p)
	if len(args) == 0 {
		usage(stdout)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return nil
	case "version":
		return printVersion(args[1:], engine, stdout)
	case "capabilities":
		return printCapabilities(engine, stdout)
	case "rpc":
		return runRPC(context.Background(), p, engine, os.Stdin, stdout)
	case "daemon", "racd":
		return runDaemon(p, engine, stdout)
	case "doctor":
		return compatDoctor(context.Background(), p, engine, args[1:], stdout, stderr)
	case "webui":
		return compatWebUI(context.Background(), p, engine, args[1:], stdout, stderr)
	case "platform":
		return compatPlatform(context.Background(), p, engine, args[1:], stdout, stderr)
	case "namespace":
		return compatNamespace(context.Background(), p, engine, args[1:], stdout, stderr)
	case "runtime":
		return runtimeCommand(context.Background(), p, engine, args[1:], stdout, stderr)
	case "migration":
		return migrationCommand(context.Background(), p, engine, args[1:], stdout, stderr)
	case "jobs", "job", "rc":
		return compatRuntime(context.Background(), p, engine, args, stdout, stderr)
	case "compat":
		if len(args) < 2 {
			return errors.New("usage: racctl compat <nexus|mountctl> ...")
		}
		switch args[1] {
		case "nexus":
			return compatNexus(context.Background(), p, engine, args[2:], stdout, stderr)
		case "mountctl":
			return compatMountctl(context.Background(), p, engine, args[2:], stdout, stderr)
		default:
			return fmt.Errorf("unknown compatibility surface: %s", args[1])
		}
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `Usage: racctl <command>

Commands:
  version [--json]       Print backend version/protocol metadata
  capabilities           Print machine-readable capabilities JSON
  rpc                    Read one versioned JSON request from stdin; emit NDJSON
  daemon | racd          Run the root-owned local control daemon
  doctor [--json|--bundle] Run diagnostics or create a support bundle
  webui <start|serve|bridge> Secure standalone/embedded WebUI transport
  platform ...           Root-manager, upgrade and uninstall lifecycle
  namespace ...          Inspect/preview/apply/rollback namespace visibility
  runtime ...            Inspect bundled static runtime authority
  migration ...          Inspect/preview/apply/finalize standalone provider migration
  compat nexus ...       Compatibility surface for rclone-nexus
  compat mountctl ...    Compatibility surface for rclone-mountctl`)
}

func printVersion(args []string, engine *control.Engine, w io.Writer) error {
	if len(args) == 1 && args[0] == "--json" {
		payload := map[string]any{
			"schema_version":   protocol.SchemaVersion,
			"name":             buildinfo.Name,
			"binary":           buildinfo.Binary,
			"version":          buildinfo.Version,
			"protocol":         map[string]int{"min": buildinfo.ProtocolMin, "max": buildinfo.ProtocolMax},
			"capability_count": len(engine.Capabilities().Operations),
		}
		return writeJSON(w, payload)
	}
	if len(args) != 0 {
		return errors.New("usage: racctl version [--json]")
	}
	fmt.Fprintln(w, buildinfo.Version)
	return nil
}

func printCapabilities(engine *control.Engine, w io.Writer) error {
	return writeJSON(w, engine.Capabilities())
}

func runRPC(ctx context.Context, p paths.Paths, engine *control.Engine, reader io.Reader, writer io.Writer) error {
	request, machineError := protocol.DecodeRequest(reader)
	if machineError != nil {
		response := protocol.Response{SchemaVersion: protocol.SchemaVersion, Kind: "response", RequestID: "invalid", OK: false, Error: machineError}
		payload, _ := protocol.MarshalResponse(response)
		_ = protocol.WriteNDJSON(writer, payload)
		return nil
	}
	return daemon.ProtocolStream(ctx, p, engine, request, writer)
}

func runDaemon(p paths.Paths, engine *control.Engine, w io.Writer) error {
	if _, err := platformstate.Migrate(p); err != nil {
		return fmt.Errorf("migrate platform state: %w", err)
	}
	if state, present, err := migration.Recover(context.Background(), p); err != nil {
		return fmt.Errorf("recover standalone migration (present=%t phase=%s): %w", present, state.Phase, err)
	}
	if err := journal.RecoverOrphans(p); err != nil {
		return fmt.Errorf("recover operation journal: %w", err)
	}
	server := daemon.New(p, engine)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := server.Listen(); err != nil {
		return err
	}
	fmt.Fprintf(w, "racd listening pid=%d\n", os.Getpid())
	defer server.Close()
	go supervisor.Run(ctx, p, nil)
	go daemon.RunJobScheduler(ctx, p, engine)
	go daemon.RunRuntimeUpdateScheduler(ctx, p, engine)
	return server.Serve(ctx)
}

func requestID() string {
	return "racctl-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func execute(ctx context.Context, p paths.Paths, engine *control.Engine, name, class string, args any) daemon.Result {
	return daemon.Execute(ctx, p, engine, protocol.NewRequest(requestID(), name, class, args))
}

type actionPreviewProof struct {
	CurrentRevision uint64 `json:"current_revision"`
	CandidateDigest string `json:"candidate_digest"`
	PreviewProof    string `json:"preview_proof"`
}

func actionProofFromResult(result daemon.Result, stderr io.Writer) (actionPreviewProof, error) {
	var proof actionPreviewProof
	err := humanResult(result, io.Discard, stderr, func(raw json.RawMessage) error { return json.Unmarshal(raw, &proof) })
	if err != nil {
		return proof, err
	}
	if proof.PreviewProof == "" || proof.CandidateDigest == "" {
		return proof, errors.New("backend did not issue a preview proof")
	}
	return proof, nil
}

func runtimeCommand(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: racctl runtime <command>

Commands:
  status [--json] [--require-operational]   Inspect static bundled runtime/config authority
  executable                                Print bundled module executable
  config                                    Print selected rclone.conf

Runtime import, activation, rollback, source registry and update commands were intentionally removed from the supported static-runtime product line. Build a new module ZIP with a different bundled system/bin/rclone to replace the runtime.`)
		return nil
	}
	switch args[0] {
	case "executable":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime executable")
		}
		binary, err := runtimeauth.Executable(p)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, binary)
		return nil
	case "config":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime config")
		}
		config, err := runtimeauth.ConfigPath(p)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, config)
		return nil
	case "list", "inspect", "test", "import", "manager", "source", "update", "activation-status", "activate", "rollback", "recover":
		return errors.New("static runtime build does not support runtime import, activation, rollback, source registry or update commands; rebuild and flash a module with a different bundled system/bin/rclone")
	case "status":
	default:
		return fmt.Errorf("unknown runtime command: %s", args[0])
	}
	jsonOutput := false
	requireOperational := false
	for _, arg := range args[1:] {
		switch arg {
		case "--json":
			jsonOutput = true
		case "--require-operational":
			requireOperational = true
		default:
			return errors.New("usage: racctl runtime status [--json] [--require-operational]")
		}
	}
	state, err := runtimeauth.Resolve(p)
	if err != nil {
		return err
	}
	if requireOperational {
		if _, err := runtimeauth.RequireOperational(p); err != nil {
			return err
		}
	}
	if jsonOutput {
		return writeJSON(stdout, state)
	}
	fmt.Fprintf(stdout, "mode=%s\nsource=%s\ncanonical=%t\noperational=%t\nbinary=%s\nconfig=%s\nmigration_required=%t\nambiguous_authority=%t\n", state.Mode, state.Source, state.Canonical, state.Operational, state.Binary, state.Config, state.Mode == runtimeauth.ModeMigrationRequired, state.AmbiguousAuthority)
	return nil
}

func runtimeUpdateCommand(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: racctl runtime update <command>

Commands:
  status                      Show current/staged/previous identities and retry state
  check [SOURCE_ID]           Resolve, acquire, qualify and stage according to policy
  activate                    Explicitly activate the staged qualified runtime
  rollback                    One-click rollback to the previous qualified runtime
  gc                          Prune unprotected runtime history
  policy                      Show persisted/default update policy
  policy-set [options]        Persist validated update policy
  boot-activate               Activate a staged candidate only when policy says next-reboot`)
		return nil
	}
	call := func(name, class string, payload any) error {
		result := execute(ctx, p, engine, name, class, payload)
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update status")
		}
		return call("runtime.update.status", protocol.ClassQuery, struct{}{})
	case "check":
		if len(args) > 2 {
			return errors.New("usage: racctl runtime update check [SOURCE_ID]")
		}
		sourceID := ""
		if len(args) == 2 {
			sourceID = args[1]
		}
		return call("runtime.update.check", protocol.ClassRun, map[string]any{"source_id": sourceID})
	case "retry":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update retry")
		}
		return call("runtime.update.retry", protocol.ClassRun, struct{}{})
	case "activate":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update activate")
		}
		return call("runtime.update.activate", protocol.ClassRun, struct{}{})
	case "rollback":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update rollback")
		}
		return call("runtime.update.rollback", protocol.ClassRun, struct{}{})
	case "gc":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update gc")
		}
		return call("runtime.update.gc", protocol.ClassRun, struct{}{})
	case "policy":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update policy")
		}
		return call("runtime.update.policy", protocol.ClassQuery, struct{}{})
	case "boot-activate":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime update boot-activate")
		}
		result, changed, err := runtimeupdate.BootActivate(ctx, p, nil)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"changed": changed, "result": result})
	case "policy-set":
		policy, err := runtimeupdate.LoadPolicy(p)
		if err != nil {
			return err
		}
		for i := 1; i < len(args); i++ {
			if i+1 >= len(args) {
				return fmt.Errorf("runtime update policy option %s requires a value", args[i])
			}
			key, value := args[i], args[i+1]
			i++
			boolValue := func() (bool, error) { return strconv.ParseBool(value) }
			switch key {
			case "--source":
				policy.SourceID = value
			case "--check":
				v, e := boolValue()
				if e != nil {
					return e
				}
				policy.CheckAutomatically = v
			case "--acquire":
				v, e := boolValue()
				if e != nil {
					return e
				}
				policy.AcquireAutomatically = v
			case "--qualify":
				v, e := boolValue()
				if e != nil {
					return e
				}
				policy.QualifyAutomatically = v
			case "--stage":
				v, e := boolValue()
				if e != nil {
					return e
				}
				policy.StageAutomatically = v
			case "--activation":
				policy.ActivationMode = runtimeupdate.ActivationMode(value)
			case "--restart-active-mounts":
				v, e := boolValue()
				if e != nil {
					return e
				}
				policy.RestartActiveMountsAutomatically = v
			case "--interval-minutes":
				v, e := strconv.Atoi(value)
				if e != nil {
					return e
				}
				policy.CheckIntervalMinutes = v
			case "--retain":
				v, e := strconv.Atoi(value)
				if e != nil {
					return e
				}
				policy.RetainHistory = v
			default:
				return fmt.Errorf("unknown runtime update policy option: %s", key)
			}
		}
		return call("runtime.update.policy.apply", protocol.ClassRun, policy)
	default:
		return fmt.Errorf("unknown runtime update command: %s", args[0])
	}
}

func runtimeImportRequest(args []string) (runtimestore.ImportRequest, error) {
	var req runtimestore.ImportRequest
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if i+1 >= len(args) {
			return req, fmt.Errorf("runtime import option %s requires a value", arg)
		}
		value := args[i+1]
		i++
		switch arg {
		case "--source":
			req.SourceType = runtimestore.SourceType(value)
		case "--engine":
			req.Engine = value
		case "--path":
			req.Path = value
		case "--url":
			req.URL = value
		case "--repository":
			req.Repository = value
		case "--resolved-ref":
			req.ResolvedRef = value
		case "--asset-name":
			req.AssetName = value
		case "--asset-url":
			req.AssetURL = value
		default:
			return req, fmt.Errorf("unknown runtime import option: %s", arg)
		}
	}
	if req.SourceType == "" {
		return req, errors.New("runtime import requires --source")
	}
	return req, nil
}

func runtimeSourceRegisterRequest(args []string) (runtimesource.Spec, error) {
	var spec runtimesource.Spec
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return spec, fmt.Errorf("runtime source register option %s requires a value", args[i])
		}
		arg, value := args[i], args[i+1]
		i++
		switch arg {
		case "--id":
			spec.ID = value
		case "--engine":
			spec.Engine = value
		case "--kind":
			spec.Kind = runtimesource.Kind(value)
		case "--repository":
			spec.Repository = value
		case "--channel":
			spec.DefaultChannel = runtimesource.Channel(value)
		case "--ref":
			spec.Ref = value
		case "--asset-name":
			spec.AssetName = value
		case "--asset-pattern":
			spec.AssetPattern = value
		case "--url":
			spec.URL = value
		case "--path":
			spec.Path = value
		case "--sha256":
			spec.ExpectedSHA256 = value
		case "--build-repository":
			spec.BuildRepository = value
		case "--build-required":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return spec, err
			}
			spec.BuildRequired = v
		default:
			return spec, fmt.Errorf("unknown runtime source register option: %s", arg)
		}
	}
	if spec.ID == "" || spec.Kind == "" {
		return spec, errors.New("runtime source register requires --id and --kind")
	}
	if spec.Engine == "" {
		spec.Engine = "rclone"
	}
	if spec.DefaultChannel == "" {
		switch spec.Kind {
		case runtimesource.KindGitHub, runtimesource.KindNewFuture:
			spec.DefaultChannel = runtimesource.ChannelLatestStable
		case runtimesource.KindSourceBuild:
			spec.DefaultChannel = runtimesource.ChannelPinnedCommit
		default:
			spec.DefaultChannel = runtimesource.ChannelManualOnly
		}
	}
	return spec, nil
}

func runtimeSourceResolveRequest(args []string) (runtimesource.ResolveRequest, error) {
	var req runtimesource.ResolveRequest
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return req, fmt.Errorf("runtime source resolve option %s requires a value", args[i])
		}
		arg, value := args[i], args[i+1]
		i++
		switch arg {
		case "--channel":
			req.Channel = runtimesource.Channel(value)
		case "--ref":
			req.Ref = value
		case "--asset-name":
			req.AssetName = value
		case "--asset-pattern":
			req.AssetPattern = value
		case "--sha256":
			req.ExpectedSHA256 = value
		default:
			return req, fmt.Errorf("unknown runtime source resolve option: %s", arg)
		}
	}
	if req.AssetName != "" && req.AssetPattern != "" {
		return req, errors.New("use only one of --asset-name or --asset-pattern")
	}
	return req, nil
}

func runtimeSourceCommand(ctx context.Context, p paths.Paths, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: racctl runtime source <command>

Commands:
  list                                         List builtin and custom source registry
  show SOURCE_ID                               Show one source definition
  register --id ID --kind KIND [options]       Add/update a custom source
  remove SOURCE_ID                             Remove a custom source
  resolve SOURCE_ID [options]                  Resolve mutable source to immutable provenance
  resolutions                                  List persisted immutable resolutions
  inspect-resolution RESOLUTION_ID             Verify/read one persisted resolution
  import-resolution RESOLUTION_ID              Import and qualify exactly the persisted resolution
  verify-build BUNDLE_DIR                       Verify SOURCE-X02 Android arm64 build bundle
  import-build BUNDLE_DIR                       Verify, persist source resolution, and qualify build

Kinds: github-release, url, local-binary, source-build, newfuture-derived
Channels: latest-stable, pinned-release, pinned-commit, manual-only`)
		return nil
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime source list")
		}
		items, err := runtimesource.List(p)
		if err != nil {
			return err
		}
		return writeJSON(stdout, items)
	case "show":
		if len(args) != 2 {
			return errors.New("usage: racctl runtime source show SOURCE_ID")
		}
		spec, revision, err := runtimesource.Get(p, args[1])
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"registry_revision": revision, "source": spec})
	case "register":
		spec, err := runtimeSourceRegisterRequest(args[1:])
		if err != nil {
			return err
		}
		stored, err := runtimesource.Register(p, spec)
		if err != nil {
			return err
		}
		return writeJSON(stdout, stored)
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: racctl runtime source remove SOURCE_ID")
		}
		if err := runtimesource.Remove(p, args[1]); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"removed": args[1]})
	case "resolve":
		if len(args) < 2 {
			return errors.New("usage: racctl runtime source resolve SOURCE_ID [options]")
		}
		req, err := runtimeSourceResolveRequest(args[2:])
		if err != nil {
			return err
		}
		resolution, err := runtimesource.Resolve(ctx, p, nil, args[1], req)
		if err != nil {
			return err
		}
		return writeJSON(stdout, resolution)
	case "resolutions":
		if len(args) != 1 {
			return errors.New("usage: racctl runtime source resolutions")
		}
		items, err := runtimesource.ListResolutions(p)
		if err != nil {
			return err
		}
		return writeJSON(stdout, items)
	case "inspect-resolution":
		if len(args) != 2 {
			return errors.New("usage: racctl runtime source inspect-resolution RESOLUTION_ID")
		}
		r, err := runtimesource.InspectResolution(p, args[1])
		if err != nil {
			return err
		}
		return writeJSON(stdout, r)
	case "import-resolution":
		if len(args) != 2 {
			return errors.New("usage: racctl runtime source import-resolution RESOLUTION_ID")
		}
		manifest, _, importErr := runtimeacquire.ImportResolution(ctx, p, args[1])
		if writeErr := writeJSON(stdout, manifest); writeErr != nil {
			return writeErr
		}
		return importErr
	case "verify-build":
		if len(args) != 2 {
			return errors.New("usage: racctl runtime source verify-build BUNDLE_DIR")
		}
		bundle, err := runtimebuild.VerifyBundle(args[1])
		if err != nil {
			return err
		}
		return writeJSON(stdout, bundle)
	case "import-build":
		if len(args) != 2 {
			return errors.New("usage: racctl runtime source import-build BUNDLE_DIR")
		}
		manifest, resolution, importErr := runtimebuild.ImportBundle(ctx, p, args[1])
		if writeErr := writeJSON(stdout, map[string]any{"resolution": resolution, "runtime": manifest}); writeErr != nil {
			return writeErr
		}
		return importErr
	default:
		return fmt.Errorf("unknown runtime source command: %s", args[0])
	}
}

type migrationPreviewResult struct {
	Preview struct {
		Generation      uint64 `json:"generation"`
		CandidateDigest string `json:"candidate_digest"`
		CanApply        bool   `json:"can_apply"`
		CanFinalize     bool   `json:"can_finalize"`
	} `json:"preview"`
	CurrentRevision uint64 `json:"current_revision"`
	CandidateDigest string `json:"candidate_digest"`
	PreviewProof    string `json:"preview_proof"`
}

func migrationPreviewRequest(args []string) (migration.PreviewRequest, error) {
	var req migration.PreviewRequest
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--mount":
			if i+1 >= len(args) {
				return req, errors.New("--mount requires an ID")
			}
			i++
			req.SelectedMounts = append(req.SelectedMounts, args[i])
		case "--job":
			if i+1 >= len(args) {
				return req, errors.New("--job requires an ID")
			}
			i++
			req.SelectedJobs = append(req.SelectedJobs, args[i])
		case "--job-every":
			if i+1 >= len(args) {
				return req, errors.New("--job-every requires a duration")
			}
			i++
			req.JobEvery = args[i]
		default:
			return req, fmt.Errorf("unknown migration option: %s", args[i])
		}
	}
	return req, nil
}

func migrationCommand(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: racctl migration <command>

Commands:
  inspect                                      Detect provider/config/process/mount/job state read-only
  status                                       Read durable migration state
  preview [--mount ID] [--job ID --job-every DURATION]
                                               Preview import and issue a mutation proof
  apply PROOF REVISION DIGEST [same selection options]
                                               Quiesce provider and import config/mounts/jobs disabled
  finalize-preview                             Preview authority switch after provider is explicitly disabled
  finalize PROOF REVISION DIGEST               Enable reviewed Nexus definitions and seal standalone authority
  rollback                                     Restore pre-migration Nexus state before completion
  recover                                      Recover interruption or detect competing provider authority`)
		return nil
	}
	jsonResult := func(result daemon.Result) error {
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	}
	switch args[0] {
	case "inspect":
		if len(args) != 1 {
			return errors.New("usage: racctl migration inspect")
		}
		return jsonResult(execute(ctx, p, engine, "migration.inspect", protocol.ClassQuery, struct{}{}))
	case "status":
		if len(args) != 1 {
			return errors.New("usage: racctl migration status")
		}
		return jsonResult(execute(ctx, p, engine, "migration.status", protocol.ClassQuery, struct{}{}))
	case "preview":
		req, err := migrationPreviewRequest(args[1:])
		if err != nil {
			return err
		}
		return jsonResult(execute(ctx, p, engine, "migration.preview", protocol.ClassPreview, req))
	case "apply":
		if len(args) < 4 {
			return errors.New("usage: racctl migration apply PROOF REVISION DIGEST [selection options]")
		}
		rev, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid migration revision: %w", err)
		}
		req, err := migrationPreviewRequest(args[4:])
		if err != nil {
			return err
		}
		payload := map[string]any{"request": req, "expected_revision": rev, "candidate_digest": args[3], "preview_proof": args[1]}
		return jsonResult(execute(ctx, p, engine, "migration.apply", protocol.ClassRun, payload))
	case "finalize-preview":
		if len(args) != 1 {
			return errors.New("usage: racctl migration finalize-preview")
		}
		return jsonResult(execute(ctx, p, engine, "migration.finalize.preview", protocol.ClassPreview, struct{}{}))
	case "finalize":
		if len(args) != 4 {
			return errors.New("usage: racctl migration finalize PROOF REVISION DIGEST")
		}
		rev, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid migration revision: %w", err)
		}
		payload := map[string]any{"expected_revision": rev, "candidate_digest": args[3], "preview_proof": args[1]}
		return jsonResult(execute(ctx, p, engine, "migration.finalize", protocol.ClassRun, payload))
	case "rollback":
		if len(args) != 1 {
			return errors.New("usage: racctl migration rollback")
		}
		return jsonResult(execute(ctx, p, engine, "migration.rollback", protocol.ClassRun, struct{}{}))
	case "recover":
		if len(args) != 1 {
			return errors.New("usage: racctl migration recover")
		}
		return jsonResult(execute(ctx, p, engine, "migration.recover", protocol.ClassReconcile, struct{}{}))
	default:
		return fmt.Errorf("unknown migration command: %s", args[0])
	}
}

func compatNexus(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: rclone-nexus <command>

Commands:
  status             Show provider readiness and managed mount state
  reconcile [--boot] Reconcile enabled mount definitions
  paths              Print canonical runtime paths
  config             Print credential-free configuration registry JSON
  version            Print Rclone Nexus version
  capabilities       Print backend capabilities JSON
  provider           Print provider readiness JSON
  runtime            Print canonical runtime/config authority JSON
  health [NAME]      Print supervisor health/readiness JSON
  policy [NAME]      Print resource-policy decision JSON
  vfs [NAME]         Print VFS profiles/recommendation and effective options
  cache ...          Inspect or mutate owned VFS cache
  namespace ...      Inspect/preview/apply/rollback namespace visibility
  jobs ...           Inspect/configure managed scheduled jobs
  job ...            Preview/run one managed job
  rc NAME            Read local-only rclone RC metrics
  operations [LIMIT] List persistent operation journal summaries
  operation ID       Print one persistent operation journal record
  cancel ID          Cancel a safely-cancellable active operation
  doctor             Run diagnostics
  webui ...           Start/open the secure WebUI`)
		return nil
	}
	switch args[0] {
	case "paths":
		p = p.Normalize()
		fmt.Fprintf(stdout, "module=%s\nprovider=%s\nstate=%s\nruntime=%s\nmanaged_rclone=%s\nmanaged_config=%s\nmounts=%s\nconfig=%s\ndesired=%s\nrun=%s\nlogs=%s\ncache=%s\npolicy=%s\n",
			p.ModuleDir, p.ProviderModuleDir, p.StateDir, p.RuntimeDir, p.ManagedRcloneBin, p.ManagedRcloneConfig, p.MountsDir, p.ConfigDir, p.DesiredDir, p.RunDir, p.LogDir, p.CacheDir, p.PolicyDir)
		return nil
	case "config":
		result := execute(ctx, p, engine, "config.snapshot", protocol.ClassQuery, struct{}{})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "version":
		fmt.Fprintln(stdout, buildinfo.Version)
		return nil
	case "capabilities":
		return writeJSON(stdout, engine.Capabilities())
	case "runtime":
		return runtimeCommand(ctx, p, engine, []string{"status", "--json"}, stdout, stderr)
	case "provider":
		return writeJSON(stdout, provider.Discover(p))
	case "health":
		if len(args) > 2 {
			return errors.New("usage: rclone-nexus health [NAME]")
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		result := execute(ctx, p, engine, "mount.health", protocol.ClassQuery, map[string]any{"name": name})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "policy":
		if len(args) > 2 {
			return errors.New("usage: rclone-nexus policy [NAME]")
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		result := execute(ctx, p, engine, "policy.status", protocol.ClassQuery, map[string]any{"name": name})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "vfs":
		if len(args) > 2 {
			return errors.New("usage: rclone-nexus vfs [NAME]")
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		result := execute(ctx, p, engine, "vfs.profiles", protocol.ClassQuery, map[string]any{"name": name})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "cache":
		return compatCache(ctx, p, engine, args[1:], stdout, stderr)
	case "doctor":
		return compatDoctor(ctx, p, engine, args[1:], stdout, stderr)
	case "webui":
		return compatWebUI(ctx, p, engine, args[1:], stdout, stderr)
	case "platform":
		return compatPlatform(ctx, p, engine, args[1:], stdout, stderr)
	case "namespace":
		return compatNamespace(ctx, p, engine, args[1:], stdout, stderr)
	case "jobs", "job", "rc":
		return compatRuntime(ctx, p, engine, args, stdout, stderr)
	case "operations":
		limit := 50
		if len(args) > 2 {
			return errors.New("usage: rclone-nexus operations [LIMIT]")
		}
		if len(args) == 2 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil || parsed < 1 || parsed > 200 {
				return errors.New("operations LIMIT must be 1..200")
			}
			limit = parsed
		}
		result := execute(ctx, p, engine, "operation.list", protocol.ClassQuery, map[string]any{"limit": limit})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "operation":
		if len(args) != 2 {
			return errors.New("usage: rclone-nexus operation ID")
		}
		result := execute(ctx, p, engine, "operation.status", protocol.ClassQuery, map[string]any{"request_id": args[1]})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "cancel":
		if len(args) != 2 {
			return errors.New("usage: rclone-nexus cancel ID")
		}
		result := execute(ctx, p, engine, "operation.cancel", protocol.ClassCancel, map[string]any{"request_id": args[1]})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return writeJSON(stdout, value)
		})
	case "status":
		if err := p.EnsureState(); err != nil {
			return err
		}
		// Preserve the original human CLI contract. Machine/UI clients use the
		// redacted provider.status operation instead of these root-shell paths.
		runtimeState, _ := runtimeauth.Resolve(p)
		fmt.Fprintf(stdout, "runtime_mode=%s\nruntime_source=%s\nstate_dir=%s\nrclone_config=%s\n",
			runtimeState.Mode, runtimeState.Source, p.StateDir, runtimeState.Config)
		return compatMountStatus(ctx, p, engine, "", stdout)
	case "reconcile":
		boot := len(args) > 1 && args[1] == "--boot"
		if len(args) > 1 && !boot {
			return errors.New("usage: rclone-nexus reconcile [--boot]")
		}
		result := execute(ctx, p, engine, "mount.reconcile", protocol.ClassReconcile, map[string]any{"boot": boot})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			return renderReconcile(raw, stdout, stderr)
		})
	default:
		return fmt.Errorf("unknown rclone-nexus command: %s", args[0])
	}
}

func compatWebUI(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: racctl webui <command>

Commands:
  start [--json] [--open]  Start/reuse standalone WebUI and return a one-use URL
  serve [--json|--quiet] [--idle SEC]  Run the loopback WebUI server in foreground
  bridge --capabilities | --request-base64 VALUE  Fixed embedded-manager bridge`)
		return nil
	}
	switch args[0] {
	case "start":
		jsonOutput := false
		open := false
		for _, arg := range args[1:] {
			switch arg {
			case "--json":
				jsonOutput = true
			case "--open":
				open = true
			default:
				return errors.New("usage: racctl webui start [--json] [--open]")
			}
		}
		info, err := webui.Start(ctx, p)
		if err != nil {
			return err
		}
		if open {
			if err := webui.OpenAndroid(ctx, info.BootstrapURL); err != nil {
				return err
			}
		}
		if jsonOutput {
			return writeJSON(stdout, info)
		}
		fmt.Fprintln(stdout, info.BootstrapURL)
		return nil
	case "serve":
		quiet := false
		jsonOutput := false
		idle := ""
		for index := 1; index < len(args); index++ {
			switch args[index] {
			case "--quiet":
				quiet = true
			case "--json":
				jsonOutput = true
			case "--idle":
				if index+1 >= len(args) {
					return errors.New("--idle requires seconds")
				}
				index++
				idle = args[index]
			default:
				return errors.New("usage: racctl webui serve [--json|--quiet] [--idle SEC]")
			}
		}
		duration, err := webui.ResolveIdle(p, idle)
		if err != nil {
			return err
		}
		var startup io.Writer
		if !quiet || jsonOutput {
			startup = stdout
		}
		serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		return webui.Serve(serveCtx, p, engine, duration, startup)
	case "bridge":
		if len(args) == 2 && args[1] == "--capabilities" {
			return writeJSON(stdout, engine.Capabilities())
		}
		if len(args) != 3 || args[1] != "--request-base64" {
			return errors.New("usage: racctl webui bridge <--capabilities|--request-base64 VALUE>")
		}
		envelope, err := webui.Bridge(ctx, p, engine, args[2])
		if err != nil {
			return err
		}
		return writeJSON(stdout, envelope)
	default:
		return fmt.Errorf("unknown webui command: %s", args[0])
	}
}

func compatDoctor(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) > 1 {
		return errors.New("usage: racctl doctor [--json|--bundle]")
	}
	if len(args) == 1 && args[0] == "--bundle" {
		result := execute(ctx, p, engine, "doctor.bundle", protocol.ClassRun, struct{}{})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var bundle doctor.BundleResult
			if err := json.Unmarshal(raw, &bundle); err != nil {
				return err
			}
			fmt.Fprintln(stdout, filepath.Join(p.Normalize().SupportDir, bundle.Filename))
			return nil
		})
	}
	result := execute(ctx, p, engine, "doctor.report", protocol.ClassQuery, struct{}{})
	return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
		var report doctor.Report
		if err := json.Unmarshal(raw, &report); err != nil {
			return err
		}
		if len(args) == 1 && args[0] == "--json" {
			return writeJSON(stdout, report)
		}
		if len(args) == 1 {
			return errors.New("usage: racctl doctor [--json|--bundle]")
		}
		for _, check := range report.Checks {
			fmt.Fprintf(stdout, "%-4s %-24s %s", check.Status, check.Code, check.Summary)
			if check.Detail != "" {
				fmt.Fprintf(stdout, " — %s", check.Detail)
			}
			fmt.Fprintln(stdout)
			if check.Guidance != "" && check.Status != doctor.Pass {
				fmt.Fprintf(stdout, "     next: %s\n", check.Guidance)
			}
		}
		fmt.Fprintf(stdout, "Overall: %s\n", report.Overall)
		if report.Overall == doctor.Fail {
			return &exitError{code: 2, silent: true}
		}
		return nil
	})
}

func compatPlatform(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "status" {
		result := execute(ctx, p, engine, "platform.status", protocol.ClassQuery, struct{}{})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return writeJSON(stdout, v)
		})
	}
	switch args[0] {
	case "root-manager":
		return writeJSON(stdout, rootmgr.Detect())
	case "validate-upgrade":
		if len(args) != 1 {
			return errors.New("usage: racctl platform validate-upgrade")
		}
		if err := platformstate.ValidateUpgrade(p); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "upgrade state compatible")
		return nil
	case "migrate":
		if len(args) != 1 {
			return errors.New("usage: racctl platform migrate")
		}
		result, err := platformstate.Migrate(p)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	case "verify-integrity":
		if len(args) != 1 {
			return errors.New("usage: racctl platform verify-integrity")
		}
		result := integrity.Verify(p.ModuleDir)
		if err := writeJSON(stdout, result); err != nil {
			return err
		}
		if !result.Available || !result.OK {
			return &exitError{code: 2, silent: true}
		}
		return nil
	case "purge-on-uninstall":
		if len(args) != 2 {
			return errors.New("usage: racctl platform purge-on-uninstall <enable|disable|status>")
		}
		switch args[1] {
		case "enable":
			if err := platformlifecycle.SetPurge(p, true); err != nil {
				return err
			}
		case "disable":
			if err := platformlifecycle.SetPurge(p, false); err != nil {
				return err
			}
		case "status":
		default:
			return errors.New("usage: racctl platform purge-on-uninstall <enable|disable|status>")
		}
		return writeJSON(stdout, platformlifecycle.PurgeState(p))
	case "uninstall-hook":
		if len(args) != 1 {
			return errors.New("usage: racctl platform uninstall-hook")
		}
		result, err := platformlifecycle.CleanupForUninstall(ctx, p)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	case "action":
		fmt.Fprintf(stdout, "Rclone Nexus — %s\n", rootmgr.Detect().Name)
		return compatDoctor(ctx, p, engine, nil, stdout, stderr)
	default:
		return fmt.Errorf("unknown platform command: %s", args[0])
	}
}

func compatCache(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: rclone-nexus cache <command> [name]

Commands:
  status [NAME]         Show owned cache usage/pressure
  prune-preview NAME    Preview low-water pruning
  prune NAME            Prune a stopped mount cache to configured limits
  clear-preview NAME    Preview clearing regular cache files
  clear NAME            Clear regular files from a stopped mount cache
  forget-preview NAME   Preview full owned-cache reset
  forget NAME           Reset a stopped mount cache tree`)
		return nil
	}
	var operation, class string
	payload := map[string]any{}
	switch args[0] {
	case "status":
		if len(args) > 2 {
			return errors.New("usage: rclone-nexus cache status [NAME]")
		}
		operation, class = "cache.status", protocol.ClassQuery
		if len(args) == 2 {
			payload["name"] = args[1]
		}
	case "prune-preview", "clear-preview", "forget-preview":
		if len(args) != 2 {
			return fmt.Errorf("usage: rclone-nexus cache %s NAME", args[0])
		}
		operation = "cache." + strings.TrimSuffix(args[0], "-preview") + ".preview"
		class = protocol.ClassPreview
		payload["name"] = args[1]
	case "prune", "clear", "forget":
		if len(args) != 2 {
			return fmt.Errorf("usage: rclone-nexus cache %s NAME", args[0])
		}
		operation = "cache." + args[0]
		class = protocol.ClassRun
		payload["name"] = args[1]
		if args[0] == "clear" || args[0] == "forget" {
			previewResult := execute(ctx, p, engine, operation+".preview", protocol.ClassPreview, map[string]any{"name": args[1]})
			proof, err := actionProofFromResult(previewResult, stderr)
			if err != nil {
				return err
			}
			payload["expected_revision"] = proof.CurrentRevision
			payload["candidate_digest"] = proof.CandidateDigest
			payload["preview_proof"] = proof.PreviewProof
		}
	default:
		return fmt.Errorf("unknown cache command: %s", args[0])
	}
	result := execute(ctx, p, engine, operation, class, payload)
	return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		return writeJSON(stdout, value)
	})
}

func compatRuntime(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: racctl <jobs|job|rc> ...")
	}
	switch args[0] {
	case "jobs":
		if len(args) == 1 || args[1] == "status" {
			result := execute(ctx, p, engine, "jobs.status", protocol.ClassQuery, struct{}{})
			return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
				var v any
				if err := json.Unmarshal(raw, &v); err != nil {
					return err
				}
				return writeJSON(stdout, v)
			})
		}
		if args[1] == "config" && len(args) == 2 {
			result := execute(ctx, p, engine, "jobs.snapshot", protocol.ClassQuery, struct{}{})
			return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
				var v any
				if err := json.Unmarshal(raw, &v); err != nil {
					return err
				}
				return writeJSON(stdout, v)
			})
		}
		return errors.New("usage: racctl jobs [status|config]")
	case "job":
		if len(args) != 3 {
			return errors.New("usage: racctl job <preview|run> NAME")
		}
		if args[1] == "preview" {
			result := execute(ctx, p, engine, "job.run.preview", protocol.ClassPreview, map[string]any{"name": args[2]})
			return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
				var v any
				if err := json.Unmarshal(raw, &v); err != nil {
					return err
				}
				return writeJSON(stdout, v)
			})
		}
		if args[1] == "run" {
			result := execute(ctx, p, engine, "job.run", protocol.ClassRun, map[string]any{"name": args[2], "trigger": "manual"})
			return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
				var v any
				if err := json.Unmarshal(raw, &v); err != nil {
					return err
				}
				return writeJSON(stdout, v)
			})
		}
		return errors.New("usage: racctl job <preview|run> NAME")
	case "rc":
		if len(args) != 2 {
			return errors.New("usage: racctl rc NAME")
		}
		result := execute(ctx, p, engine, "rc.metrics", protocol.ClassQuery, map[string]any{"name": args[1]})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return writeJSON(stdout, v)
		})
	}
	return fmt.Errorf("unknown runtime command: %s", args[0])
}

func compatNamespace(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: racctl namespace <command> [name]

Commands:
  inspect NAME        Report observed service/shell/zygote/app visibility
  preview NAME        Preview app-visibility namespace mutations
  apply NAME          Persist app-visible intent and apply qualified binds
  rollback-preview NAME Preview release of Nexus-owned binds
  rollback NAME       Release Nexus-owned binds and return to root-only visibility
  reconcile [NAME]    Reconcile persisted visibility after namespace churn`)
		return nil
	}
	var operation, class string
	payload := map[string]any{}
	switch args[0] {
	case "inspect":
		if len(args) != 2 {
			return errors.New("usage: racctl namespace inspect NAME")
		}
		operation, class, payload["name"] = "namespace.inspect", protocol.ClassQuery, args[1]
	case "preview":
		if len(args) != 2 {
			return errors.New("usage: racctl namespace preview NAME")
		}
		operation, class, payload["name"] = "namespace.preview", protocol.ClassPreview, args[1]
	case "apply":
		if len(args) != 2 {
			return errors.New("usage: racctl namespace apply NAME")
		}
		operation, class, payload["name"] = "namespace.apply", protocol.ClassRun, args[1]
		previewResult := execute(ctx, p, engine, "namespace.preview", protocol.ClassPreview, map[string]any{"name": args[1]})
		proof, err := actionProofFromResult(previewResult, stderr)
		if err != nil {
			return err
		}
		payload["expected_revision"] = proof.CurrentRevision
		payload["candidate_digest"] = proof.CandidateDigest
		payload["preview_proof"] = proof.PreviewProof
	case "rollback-preview":
		if len(args) != 2 {
			return errors.New("usage: racctl namespace rollback-preview NAME")
		}
		operation, class, payload["name"] = "namespace.rollback.preview", protocol.ClassPreview, args[1]
	case "rollback":
		if len(args) != 2 {
			return errors.New("usage: racctl namespace rollback NAME")
		}
		operation, class, payload["name"] = "namespace.rollback", protocol.ClassRun, args[1]
		previewResult := execute(ctx, p, engine, "namespace.rollback.preview", protocol.ClassPreview, map[string]any{"name": args[1]})
		proof, err := actionProofFromResult(previewResult, stderr)
		if err != nil {
			return err
		}
		payload["expected_revision"] = proof.CurrentRevision
		payload["candidate_digest"] = proof.CandidateDigest
		payload["preview_proof"] = proof.PreviewProof
	case "reconcile":
		if len(args) > 2 {
			return errors.New("usage: racctl namespace reconcile [NAME]")
		}
		operation, class = "namespace.reconcile", protocol.ClassReconcile
		if len(args) == 2 {
			payload["name"] = args[1]
		}
	default:
		return fmt.Errorf("unknown namespace command: %s", args[0])
	}
	result := execute(ctx, p, engine, operation, class, payload)
	return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		return writeJSON(stdout, value)
	})
}

func compatMountctl(ctx context.Context, p paths.Paths, engine *control.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, `Usage: rclone-mountctl <command> [name]

Commands:
  list              List configured mounts
  status [name]     Show one or all mount states
  start NAME        Start a configured mount
  stop NAME         Stop a managed mount
  restart NAME      Restart a managed mount
  reconcile         Start every enabled mount that is not running`)
		return nil
	}
	if err := p.EnsureState(); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: rclone-mountctl list")
		}
		result := execute(ctx, p, engine, "mount.list", protocol.ClassQuery, struct{}{})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var payload struct {
				Mounts []string `json:"mounts"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			for _, name := range payload.Mounts {
				fmt.Fprintln(stdout, name)
			}
			return nil
		})
	case "status":
		if len(args) > 2 {
			return errors.New("usage: rclone-mountctl status [name]")
		}
		name := ""
		if len(args) == 2 {
			name = args[1]
		}
		return compatMountStatus(ctx, p, engine, name, stdout)
	case "start", "stop", "restart":
		if len(args) != 2 {
			return fmt.Errorf("usage: rclone-mountctl %s NAME", args[0])
		}
		name := args[1]
		operation := "mount." + args[0]
		result := execute(ctx, p, engine, operation, protocol.ClassRun, map[string]any{"name": name})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			var payload mounts.ActionResult
			if err := json.Unmarshal(raw, &payload); err != nil {
				return err
			}
			switch args[0] {
			case "start", "restart":
				if payload.Noop {
					fmt.Fprintf(stdout, "%s already running (pid %d)\n", name, payload.PID)
				} else {
					fmt.Fprintf(stdout, "%s started (pid %d)\n", name, payload.PID)
				}
			case "stop":
				fmt.Fprintf(stdout, "%s stopped\n", name)
			}
			return nil
		})
	case "reconcile":
		if len(args) != 1 {
			return errors.New("usage: rclone-mountctl reconcile")
		}
		result := execute(ctx, p, engine, "mount.reconcile", protocol.ClassReconcile, struct{}{})
		return humanResult(result, stdout, stderr, func(raw json.RawMessage) error {
			return renderReconcile(raw, stdout, stderr)
		})
	default:
		return fmt.Errorf("unknown rclone-mountctl command: %s", args[0])
	}
}

func renderReconcile(raw json.RawMessage, stdout, stderr io.Writer) error {
	var payload struct {
		Changed  []mounts.ActionResult     `json:"changed"`
		Failures []mounts.LifecycleFailure `json:"failures"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	for _, item := range payload.Changed {
		switch item.State {
		case "started", "running":
			fmt.Fprintf(stdout, "%s started (pid %d)\n", item.Name, item.PID)
		case "stopped":
			fmt.Fprintf(stdout, "%s stopped\n", item.Name)
		default:
			fmt.Fprintf(stdout, "%s %s\n", item.Name, item.State)
		}
	}
	for _, failure := range payload.Failures {
		fmt.Fprintf(stderr, "rclone-nexus: %s: %s\n", failure.Name, failure.Error)
	}
	if len(payload.Failures) != 0 {
		return &exitError{code: 1, silent: true}
	}
	return nil
}

func compatMountStatus(ctx context.Context, p paths.Paths, engine *control.Engine, name string, stdout io.Writer) error {
	result := execute(ctx, p, engine, "mount.status", protocol.ClassQuery, map[string]any{"name": name})
	if !result.Response.OK {
		return &exitError{code: 1, silent: true}
	}
	raw, err := json.Marshal(result.Response.Result)
	if err != nil {
		return err
	}
	if name != "" {
		var status mounts.Status
		if err := json.Unmarshal(raw, &status); err != nil {
			return err
		}
		printMountStatus(stdout, status)
		if status.State == "running" {
			return nil
		}
		if status.State == "not-configured" {
			return &exitError{code: 2, silent: true}
		}
		return &exitError{code: 1, silent: true}
	}
	var payload struct {
		Mounts []mounts.Status `json:"mounts"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if len(payload.Mounts) == 0 {
		fmt.Fprintln(stdout, "no mounts configured")
		return nil
	}
	allRunning := true
	for _, status := range payload.Mounts {
		printMountStatus(stdout, status)
		if status.State != "running" {
			allRunning = false
		}
	}
	if !allRunning {
		return &exitError{code: 1, silent: true}
	}
	return nil
}

func printMountStatus(w io.Writer, status mounts.Status) {
	switch status.State {
	case "running":
		fmt.Fprintf(w, "%s\trunning\tpid=%d\n", status.Name, status.PID)
	case "not-configured":
		fmt.Fprintf(w, "%s\tnot-configured\n", status.Name)
	case "stale":
		fmt.Fprintf(w, "%s\tstale\n", status.Name)
	default:
		fmt.Fprintf(w, "%s\tstopped\n", status.Name)
	}
}

type exitError struct {
	code    int
	message string
	silent  bool
}

func (e *exitError) Error() string {
	if e.message != "" {
		return e.message
	}
	return fmt.Sprintf("exit status %d", e.code)
}

func humanResult(result daemon.Result, stdout, stderr io.Writer, render func(json.RawMessage) error) error {
	if !result.Response.OK {
		if result.Response.Error != nil {
			fmt.Fprintf(stderr, "rclone-nexus: %s\n", result.Response.Error.Message)
			if result.Response.Error.Detail != "" {
				fmt.Fprintf(stderr, "detail: %s\n", result.Response.Error.Detail)
			}
		}
		return &exitError{code: 1, silent: true}
	}
	raw, err := json.Marshal(result.Response.Result)
	if err != nil {
		return err
	}
	return render(raw)
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

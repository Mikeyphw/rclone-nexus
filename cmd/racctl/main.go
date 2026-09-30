package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"rclone-nexus/internal/buildinfo"
	"rclone-nexus/internal/control"
	"rclone-nexus/internal/daemon"
	"rclone-nexus/internal/journal"
	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/supervisor"
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
	return server.Serve(ctx)
}

func requestID() string {
	return "racctl-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func execute(ctx context.Context, p paths.Paths, engine *control.Engine, name, class string, args any) daemon.Result {
	return daemon.Execute(ctx, p, engine, protocol.NewRequest(requestID(), name, class, args))
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
  health [NAME]      Print supervisor health/readiness JSON
  operations [LIMIT] List persistent operation journal summaries
  operation ID       Print one persistent operation journal record
  cancel ID          Cancel a safely-cancellable active operation
  doctor             Run diagnostics`)
		return nil
	}
	switch args[0] {
	case "paths":
		p = p.Normalize()
		fmt.Fprintf(stdout, "module=%s\nprovider=%s\nstate=%s\nmounts=%s\nconfig=%s\ndesired=%s\nrun=%s\nlogs=%s\ncache=%s\n",
			p.ModuleDir, p.ProviderModuleDir, p.StateDir, p.MountsDir, p.ConfigDir, p.DesiredDir, p.RunDir, p.LogDir, p.CacheDir)
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
		fmt.Fprintf(stdout, "provider_module=%s\nstate_dir=%s\nrclone_config=%s\n",
			p.ProviderModuleDir, p.StateDir, p.RcloneConfig)
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
	case "doctor":
		return fmt.Errorf("doctor is provided by the rclone-doctor compatibility launcher")
	default:
		return fmt.Errorf("unknown rclone-nexus command: %s", args[0])
	}
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

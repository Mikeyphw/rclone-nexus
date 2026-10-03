#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path
import os
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def main() -> int:
    rc = read("internal/rc/rc.go")
    require("--rc-no-open-browser" not in rc, "obsolete --rc-no-open-browser remains in production RC argv")
    require('"--rc", "--rc-addr"' in rc, "expected loopback RC argv contract missing")

    provider = read("internal/provider/provider.go")
    require('"system", "vendor", "bin", "rclone"' in provider, "NewFuture system/vendor/bin/rclone candidate missing")
    require('"system", "vendor", "bin", "fusermount3"' in provider, "NewFuture system/vendor/bin/fusermount3 candidate missing")
    find_rclone = provider.index("func FindRclone")
    look_path = provider.index('exec.LookPath("rclone")', find_rclone)
    vendor_path = provider.index('"system", "vendor", "bin", "rclone"', find_rclone)
    require(vendor_path < look_path, "host PATH rclone still outranks NewFuture provider binary")

    cli = read("internal/provider/cli.go")
    require("UnsupportedMountFlagsForBinary" in cli and '"mount", "--help"' in cli, "provider CLI preflight is missing exact-binary qualification")
    require('"help", "flags"' in cli and '"--help"' in cli and "helpParts" in cli, "provider CLI preflight does not merge command and global rclone help")
    require("context.WithTimeout" in cli and "limitedBuffer" in cli, "provider CLI preflight is not bounded")

    common = read("module/lib/common.sh")
    require("$RNEXUS_PROVIDER_MODULE_DIR/system/vendor/bin/rclone" in common, "shell provider discovery misses system/vendor/bin")
    require('set -a' in common and '. "$RNEXUS_PROVIDER_MODULE_DIR/env"' in common and 'set +a' in common, "provider top-level env is not exported to child processes")
    require("${RCLONE_CONFIG:-" in common and "export RCLONE_CONFIG" in common, "provider RCLONE_CONFIG is not authoritative/exported in shell fallback")

    # Execute the shell boundary, not just string-match it. NewFuture's env may
    # define values itself and source conf/env through MODPATH; all of those must
    # reach the racctl/rclone child environment.
    with tempfile.TemporaryDirectory(prefix="rnexus-provider-env-") as td:
        root = Path(td)
        provider_dir = root / "provider"
        nexus_dir = root / "nexus"
        (provider_dir / "conf").mkdir(parents=True)
        nexus_dir.mkdir()
        (provider_dir / "env").write_text(
            'RCLONE_CONFIG="$MODPATH/conf/custom.conf"\n'
            'RCLONE_CONFIG_PASS=provider-secret-canary\n'
            '[ -f "$MODPATH/conf/env" ] && . "$MODPATH/conf/env"\n',
            encoding="utf-8",
        )
        (provider_dir / "conf" / "env").write_text('HTTPS_PROXY=http://proxy.invalid\n', encoding="utf-8")
        script = f'. {str(ROOT / "module/lib/common.sh")!s}; env'
        shell = shutil.which("sh") or "/system/bin/sh"
        cp = subprocess.run(
            [shell, "-c", script],
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env={**os.environ, "RNEXUS_PROVIDER_MODULE_DIR": str(provider_dir), "RNEXUS_MODULE_DIR": str(nexus_dir)},
            check=False,
        )
        require(cp.returncode == 0, f"common.sh provider env execution failed: {cp.stderr.strip()}")
        exported = dict(line.split("=", 1) for line in cp.stdout.splitlines() if "=" in line)
        require(exported.get("RCLONE_CONFIG") == str(provider_dir / "conf" / "custom.conf"), "provider RCLONE_CONFIG did not reach child environment")
        require(exported.get("RCLONE_CONFIG_PASS") == "provider-secret-canary", "provider credential env did not reach child environment")
        require(exported.get("HTTPS_PROXY") == "http://proxy.invalid", "provider conf/env override did not reach child environment")

    action = read("module/action.sh")
    doctor = read("module/system/bin/rclone-doctor")
    require('lib/common.sh' in action and 'rnexus_racctl_bin' in action, "embedded-manager action bypasses provider env wrapper")
    require('lib/common.sh' in doctor and 'rnexus_racctl_bin' in doctor, "doctor entry point bypasses provider env wrapper")

    lifecycle = read("internal/mounts/lifecycle.go")
    for token in ["type LifecycleError struct", "Stage", "Retryable", "ExitCode", "UnsupportedMountFlagsForBinary", "startupLifecycleError", "waitCh", "lifecycleDetail"]:
        require(token in lifecycle, f"lifecycle structured startup contract missing {token}")
    for code in ["mount_config_invalid", "provider_unavailable", "provider_config_missing", "mount_args_invalid", "provider_cli_probe_failed", "rclone_cli_incompatible"]:
        require(code in lifecycle, f"terminal lifecycle classification missing {code}")
    require("diagnostics.SanitizeText" in lifecycle, "startup structured details are not sanitized")

    protocol = read("internal/protocol/types.go")
    require('`json:"stage,omitempty"`' in protocol and '`json:"exit_code,omitempty"`' in protocol, "MachineError does not carry lifecycle stage/exit code")

    engine = read("internal/control/engine.go")
    require("errors.As(err, &lifecycleErr)" in engine, "control layer does not preserve LifecycleError")
    require("Stage: lifecycleErr.Stage" in engine and "ExitCode: lifecycleErr.ExitCode" in engine, "control layer loses lifecycle stage/exit code")

    supervisor = read("internal/supervisor/supervisor.go")
    require("FailureCode" in supervisor and "Retryable" in supervisor, "supervisor health lacks structured failure truth")
    require('if h.FailureCode != "" && !h.Retryable' in supervisor, "terminal lifecycle failure is not preserved ahead of readiness")
    require("errors.As(err, &lifecycleErr)" in supervisor, "supervisor does not classify LifecycleError retryability")
    require("readinessTerminalFailure" in supervisor and '"provider_config_missing"' in supervisor, "provider readiness/config failures are not terminal/non-budgeted")

    readiness = read("internal/readiness/readiness.go")
    require("networkRequired := spec.RequireNetwork" in readiness, "ProbeRemote still incorrectly makes network mandatory")
    require('"offline_allowed"' in readiness, "offline-allowed cold-start contract missing")
    require("defaultIPv6Interface" in readiness and "activeNetworkInterface" in readiness, "IPv6/VPN network fallback missing")

    diagnostics = read("internal/diagnostics/read.go")
    require("parseRuntimeLogLine" in diagnostics, "rclone timestamp/severity parser missing")
    require("cliHelpLine" in diagnostics and "suppressed" in diagnostics.lower(), "CLI help collapse missing")
    # The old semantic bug classified any occurrence of error/warn in arbitrary text.
    require('strings.Contains(lower, "error")' not in diagnostics, "raw-log severity still substring-classifies arbitrary 'error' text")

    html = read("module/webroot/index.html")
    for control_id in ["logSeverityFilter", "logSourceFilter", "logSearch"]:
        require(f'id="{control_id}"' in html, f"log investigation control {control_id} missing")

    app = read("module/webroot/app.js")
    for token in ["sourceReady", "not applicable", "failure_code", "terminalFailure", "retryableFailure", "Start blocked", "View logs", "View operations", "scrollIntoView", "logSeverityFilter", "logSourceFilter", "logSearch"]:
        require(token in app, f"WebUI runtime/log contract missing {token}")
    require("start.disabled = running || terminalFailure" in app, "terminal persistent mount failures still expose clickable retry/start")
    require("compatibility.hidden = true" in app, "healthy compatibility banner is not collapsed")
    require("error.stage" in app and "error.exitCode" in app, "WebUI does not preserve structured lifecycle stage/exit code")

    css = read("module/webroot/style.css")
    require("safe-area-inset-top" in css and "safe-area-inset-left" in css and "safe-area-inset-right" in css, "Android safe-area handling missing")
    require("scroll-snap-type" in css, "mobile navigation does not expose scroll/snap affordance")

    test_tokens = {
        "internal/provider/cli_test.go": "TestUnsupportedMountFlagsUsesCommandAndGlobalProviderHelp",
        "internal/provider/cli_test.go#root-global-fallback": "TestUnsupportedMountFlagsFallsBackToRootHelpForGlobalFlags",
        "internal/provider/cli_test.go#exact-selected": "TestUnsupportedMountFlagsForBinaryUsesExactSelectedExecutable",
        "internal/provider/provider_layout_test.go": "TestProviderModuleWinsOverHostPath",
        "internal/readiness/readiness_test.go": "TestOfflineAllowedProbeDoesNotBlockColdStartWithoutNetwork",
        "internal/diagnostics/read_test.go": "TestRcloneHelpTextIsNotMisclassifiedAndIsCollapsed",
        "internal/supervisor/supervisor_test.go": "TestTerminalCLICompatibilityFailureDoesNotConsumeRestartBudget",
        "internal/supervisor/supervisor_test.go#config": "TestMissingProviderConfigIsTerminalWithoutRestartBudgetAndRecovers",
        "internal/supervisor/supervisor_test.go#network": "TestTransientStartupNetworkFailureConsumesRetryBudget",
        "internal/control/engine_test.go": "TestMapErrorPreservesStructuredLifecycleFailure",
    }
    for path, token in test_tokens.items():
        real_path = path.split("#", 1)[0]
        require(token in read(real_path), f"regression evidence missing: {token}")

    install = read("scripts/dev/install_stack.py")
    require('"provider": [str(nexus_wrapper), "provider"]' in install and '"health": [str(nexus_wrapper), "health"]' in install, "install-verify bypasses provider env-loading wrapper")

    print("GRAND-G1 device runtime + WebUI remediation contract: PASS")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)

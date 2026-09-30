#!/usr/bin/env python3
from __future__ import annotations

import os
from pathlib import Path
import signal
import subprocess

ROOT = Path(__file__).resolve().parents[2]
PACKAGES = [
    "./internal/mounts",
    "./internal/supervisor",
    "./internal/namespace",
    "./internal/jobs",
    "./internal/platformstate",
    "./internal/platformlifecycle",
    "./internal/doctor",
    "./internal/webui",
    "./internal/rc",
]
PATTERN = "|".join([
    "TestAtomicRegistryFailureKeepsPublishedFileIntact",
    "TestConcurrentStartStopDoesNotCorruptOwnership",
    "TestCoreG1StopNeverUnmountsUnownedOrReusedPIDMount",
    "TestSupervisorRepairsStaleOwnedMount",
    "TestOfflineDoesNotDestroyValidMount",
    "TestRestartBudgetExhaustionIsPersistent",
    "TestBootWaitCancelsCleanly",
    "TestApplyTransactionRollsBackPartialBindsAndState",
    "TestApplyCancellationRollsBackCreatedBindAndRestoresState",
    "TestPreviewFailsClosedWhenTopologyIsTruncated",
    "TestScheduledClaimMovesNextRunBeforeProcessAndPreventsImmediateDuplicate",
    "TestConcurrentRegistryApplyAllowsOnlyOneRevisionWinner",
    "TestPolicyBlockedJobDoesNotLaunchProvider",
    "TestFailedMigrationRestoresOriginal",
    "TestMigrationPreservesPersistentUserState",
    "TestUninstallPreservesPersistentStateUnlessExplicitlyArmed",
    "TestSupportBundleRedactsSecretsAndPrivatePaths",
    "TestSupportBundleIsDeterministicForSameInputs",
    "TestStandaloneBootstrapSecurityAndTypedRoute",
    "TestHostBoundsAndRuntimeStatePermissions",
    "TestEmbeddedBridgeRejectsUnknownAndClassMismatch",
    "TestPrepareLoopbackAndCredentialsStayPrivate",
    "TestMetricsAuthFailureDegradesWithoutCredentialEcho",
])
REPEAT_PATTERN = "|".join([
    "TestConcurrentStartsConvergeToOneManagedProcess",
    "TestCoreG1RepeatedLifecycleAndReconcileCycles",
    "TestSchedulerDoesNotDuplicateDueJob",
    "TestSchedulerRestartDoesNotReplayAlreadyClaimedRun",
])


def run_group(argv: list[str], timeout: int) -> None:
    proc = subprocess.Popen(argv, cwd=ROOT, start_new_session=True)
    try:
        rc = proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=5)
        raise SystemExit(f"failure-injection command timed out after {timeout}s: {' '.join(argv)}")
    if rc != 0:
        raise SystemExit(f"failure-injection command failed ({rc}): {' '.join(argv)}")


run_group(["go", "test", "-count=1", "-timeout=45s", "-run", PATTERN, *PACKAGES], 70)
run_group(["go", "test", "-count=3", "-timeout=60s", "-run", REPEAT_PATTERN, "./internal/mounts", "./internal/daemon"], 90)
print("release failure injection: OK")

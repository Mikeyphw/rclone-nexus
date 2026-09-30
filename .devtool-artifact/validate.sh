#!/usr/bin/env sh
set -eu
cd "${DEVTOOL_REPO_ROOT:?}"
export PYTHONDONTWRITEBYTECODE=1

# Apply-time validation must be satisfiable by the candidate overlay itself.
# Real-device schema-v3 evidence is intentionally created only after this
# overlay commits, so the authoritative release seal remains a separate,
# fail-closed post-commit workflow.
python3 -m unittest tests.test_release_qualification tests.test_grand_g1 -v
python3 scripts/dev/grand_g1_gate.py --source-only

echo "GRAND-G1 source/harness qualification passed."
echo "The release is NOT sealed yet. After commit, run ./devtoolw release-evidence,"
echo "complete run/resume device qualification, then run ./devtoolw release."

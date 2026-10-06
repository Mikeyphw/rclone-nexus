package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRuntimeStoreCommandsAreUnsupportedInStaticBuild(t *testing.T) {
	for _, args := range [][]string{
		{"runtime", "import", "--source", "local-file", "--engine", "rclone", "--path", "/tmp/rclone"},
		{"runtime", "inspect", "runtime-id"},
		{"runtime", "list"},
		{"runtime", "test", "runtime-id"},
		{"runtime", "manager"},
		{"runtime", "source", "list"},
		{"runtime", "update", "status"},
		{"runtime", "activation-status"},
		{"runtime", "activate", "runtime-id"},
		{"runtime", "rollback"},
		{"runtime", "recover"},
	} {
		var stdout, stderr bytes.Buffer
		err := run(args, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "static runtime build does not support") {
			t.Fatalf("%v returned err=%v stdout=%q stderr=%q", args, err, stdout.String(), stderr.String())
		}
	}
}

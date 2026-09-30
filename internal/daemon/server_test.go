package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
)

func daemonPaths(t *testing.T) paths.Paths {
	// Use the platform temporary directory rather than hard-coding /tmp. Android/Termux
	// does not provide a writable /tmp, while os.MkdirTemp("", ...) honors TMPDIR.
	// Keep the generated prefix short so Unix-domain socket paths remain below
	// sockaddr_un limits even when validation runs from a long Devtool worktree.
	base, err := os.MkdirTemp("", "rnx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	p := paths.Paths{StateDir: filepath.Join(base, "state"), MountsDir: filepath.Join(base, "state", "mounts.d"), RunDir: filepath.Join(base, "state", "run"), LogDir: filepath.Join(base, "state", "logs"), CacheDir: filepath.Join(base, "state", "cache")}
	p.Socket = filepath.Join(p.RunDir, "racd.sock")
	p.DaemonLock = filepath.Join(p.RunDir, "racd.lock")
	return p
}

func TestSingleInstanceLockAndCleanClose(t *testing.T) {
	p := daemonPaths(t)
	first := New(p, control.New(p))
	if err := first.Listen(); err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second := New(p, control.New(p))
	err := second.Listen()
	if err == nil || !strings.Contains(err.Error(), "RNX_E_DAEMON_ALREADY_RUNNING") {
		t.Fatalf("expected singleton error, got %v", err)
	}
	if info, err := os.Stat(p.Socket); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: info=%v err=%v", info, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Socket); !os.IsNotExist(err) {
		t.Fatalf("socket survived close: %v", err)
	}
}

func TestServeStopsWhenContextCancelled(t *testing.T) {
	p := daemonPaths(t)
	server := New(p, control.New(p))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(p.Socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

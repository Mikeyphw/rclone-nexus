package webui

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/daemon"
	"rclone-nexus/internal/diagnostics"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

const maxBridgeEncodedBytes = 96 << 10

func StaticDir(p paths.Paths) string {
	p = p.Normalize()
	return filepath.Join(p.ModuleDir, "webroot")
}

func Serve(ctx context.Context, p paths.Paths, engine *control.Engine, idle time.Duration, startup io.Writer) error {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return err
	}
	logf := func(format string, args ...any) {
		_ = diagnostics.Append(p, "webui", "standalone", "event", "WEBUI_EVENT", map[string]any{"message": fmt.Sprintf(format, args...)})
	}
	server, err := New(Config{Paths: p, Engine: engine, StaticDir: StaticDir(p), IdleTimeout: idle, Logf: logf})
	if err != nil {
		return err
	}
	info, err := server.Start(ctx)
	if err != nil {
		return err
	}
	if startup != nil {
		payload, _ := json.Marshal(info)
		_, _ = startup.Write(append(payload, '\n'))
	}
	server.Wait()
	return nil
}

// Start reuses a live server when possible; otherwise it detaches a new owned
// racctl webui serve process and waits only for its private runtime state.
func Start(ctx context.Context, p paths.Paths) (Info, error) {
	p = p.Normalize()
	if info, err := IssueFromExisting(ctx, p); err == nil {
		return info, nil
	}
	_ = os.Remove(filepath.Join(p.RunDir, stateFileName))
	executable, err := os.Executable()
	if err != nil {
		return Info{}, err
	}
	if err := p.EnsureState(); err != nil {
		return Info{}, err
	}
	logFile, err := os.OpenFile(filepath.Join(p.LogDir, "webui-launch.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Info{}, err
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "webui", "serve", "--quiet")
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Info{}, err
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(4 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return Info{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		info, err := IssueFromExisting(ctx, p)
		if err == nil {
			return info, nil
		}
		lastErr = err
	}
	return Info{}, fmt.Errorf("webui server did not become ready: %w", lastErr)
}

func OpenAndroid(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.Contains(url, "/bootstrap?token=") {
		return errors.New("refusing to open non-loopback or non-bootstrap URL")
	}
	am := "/system/bin/am"
	if value := strings.TrimSpace(os.Getenv("RNEXUS_ANDROID_AM")); value != "" {
		am = value
	}
	command := exec.CommandContext(ctx, am, "start", "-a", "android.intent.action.VIEW", "-d", url)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

// Bridge accepts only a base64url-encoded protocol Request. The operation name
// and class are still checked by the native Engine registry; no command, path,
// shell text or argv is accepted from the browser transport.
func Bridge(ctx context.Context, p paths.Paths, engine *control.Engine, encoded string) (Envelope, error) {
	if len(encoded) == 0 || len(encoded) > maxBridgeEncodedBytes {
		return Envelope{}, errors.New("embedded request is empty or too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Envelope{}, errors.New("embedded request is not valid base64url")
	}
	if len(raw) > protocol.MaxRequestBytes {
		return Envelope{}, errors.New("embedded request exceeds protocol limit")
	}
	request, machineErr := protocol.DecodeRequest(strings.NewReader(string(raw)))
	if machineErr != nil {
		return Envelope{}, fmt.Errorf("embedded request rejected: %s", machineErr.Code)
	}
	descriptor, ok := engine.Descriptor(request.Operation.Name)
	if !ok || descriptor.Class != request.Operation.Class {
		return Envelope{}, errors.New("embedded operation is not allow-listed")
	}
	result := daemon.Execute(ctx, p, engine, request)
	envelope := Envelope{SchemaVersion: 1, Response: result.Response, Events: result.Events}
	encodedEnvelope, err := json.Marshal(envelope)
	if err != nil {
		return Envelope{}, err
	}
	if len(encodedEnvelope) > protocol.MaxResponseBytes {
		return Envelope{}, errors.New("embedded response exceeds protocol limit")
	}
	return envelope, nil
}

func EncodeRequest(request protocol.Request) (string, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	if len(raw) > protocol.MaxRequestBytes {
		return "", errors.New("request exceeds protocol limit")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func ParseIdle(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return defaultIdle, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 5 || seconds > 3600 {
		return 0, errors.New("idle timeout must be 5..3600 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

func ReadStartup(reader io.Reader) (Info, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 32<<10))
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Info{}, err
		}
		return Info{}, io.EOF
	}
	var info Info
	if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
		return Info{}, err
	}
	return info, nil
}

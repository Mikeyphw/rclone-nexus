package runtimestore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"rclone-nexus/internal/mounts"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/provider"
	"rclone-nexus/internal/redact"
)

var secretPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|cookie|client_secret|refresh_token|access_token|api_key|apikey)([=: ]+)([^\s&]+)`)
var versionPattern = regexp.MustCompile(`(?i)^(rclone|bclone)\s+v?[0-9]+(?:\.[0-9]+)?`)

func sanitizeDetail(value string) string {
	value = secretPattern.ReplaceAllString(value, `$1$2<redacted>`)
	return redact.BoundedString(strings.TrimSpace(value), 2048)
}

func addCheck(q *Qualification, name, status string, required bool, detail string) {
	q.Checks = append(q.Checks, Check{Name: name, Status: status, Required: required, Detail: sanitizeDetail(detail)})
}

func androidDevice() (bool, string) {
	getprop := "/system/bin/getprop"
	if info, err := os.Stat(getprop); err != nil || !info.Mode().IsRegular() {
		return false, "android getprop is unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, getprop, "ro.build.version.sdk").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return false, "android build identity is unavailable"
	}
	return true, "android sdk " + strings.TrimSpace(string(out))
}

func architectureCompatible(android bool, hostArch, candidateArch string) error {
	if android && hostArch == "arm64" && candidateArch != "arm64" {
		return errors.New("Android device requires arm64 candidate")
	}
	return nil
}

func fileHashFromOpen(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type pinnedBinary struct {
	file *os.File
	path string
	hash string
}

func openPinned(path, expectedHash string) (*pinnedBinary, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("candidate is not an executable regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	digest, err := fileHashFromOpen(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if expectedHash != "" && digest != expectedHash {
		f.Close()
		return nil, errors.New("candidate bytes changed before qualification")
	}
	return &pinnedBinary{file: f, path: path, hash: digest}, nil
}

func (p *pinnedBinary) Close() error { return p.file.Close() }

func (p *pinnedBinary) command(ctx context.Context, args ...string) *exec.Cmd {
	// Execute the already-open inode through a child-visible descriptor. The
	// pathname can be replaced or deleted after open without changing the bytes
	// the qualifier launches.
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	cmd.Args[0] = p.path
	cmd.ExtraFiles = []*os.File{p.file}
	return cmd
}

func runPinned(ctx context.Context, binary *pinnedBinary, limit int, args ...string) (string, error) {
	cmd := binary.command(ctx, args...)
	var buf bytes.Buffer
	cmd.Stdout = &limitedWriter{W: &buf, Remaining: limit}
	cmd.Stderr = &limitedWriter{W: &buf, Remaining: limit}
	err := cmd.Run()
	return sanitizeDetail(buf.String()), err
}

type limitedWriter struct {
	W         io.Writer
	Remaining int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	original := len(p)
	if w.Remaining <= 0 {
		return original, nil
	}
	chunk := p
	if len(chunk) > w.Remaining {
		chunk = chunk[:w.Remaining]
	}
	_, err := w.W.Write(chunk)
	w.Remaining -= len(chunk)
	return original, err
}

func versionCheck(ctx context.Context, binary *pinnedBinary) (string, error) {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runPinned(probe, binary, 32<<10, "version")
	if err != nil {
		return out, err
	}
	line := strings.TrimSpace(strings.Split(out, "\n")[0])
	if !versionPattern.MatchString(line) {
		return line, errors.New("version output does not identify an rclone-compatible engine")
	}
	return line, nil
}

func configEnumerationCheck(ctx context.Context, binary *pinnedBinary, work string) error {
	config := filepath.Join(work, "rclone.conf")
	if err := os.WriteFile(config, []byte("[nexus_qual]\ntype = local\n"), 0o600); err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := runPinned(probe, binary, 64<<10, "listremotes", "--config", config)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "nexus_qual:" {
			return nil
		}
	}
	return errors.New("candidate did not enumerate the qualification remote")
}

func helpContainsFlag(help, flag string) bool {
	for _, line := range strings.Split(help, "\n") {
		for _, token := range strings.Fields(line) {
			candidate := strings.TrimRight(token, ",")
			if candidate == flag || strings.HasPrefix(candidate, flag+"=") {
				return true
			}
		}
	}
	return false
}

func cliContractCheck(ctx context.Context, binary *pinnedBinary, work string) error {
	probe, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	mountHelp, err := runPinned(probe, binary, 512<<10, "mount", "--help")
	if err != nil {
		return fmt.Errorf("mount help failed: %w", err)
	}
	help := mountHelp
	if text, helpErr := runPinned(probe, binary, 512<<10, "help", "flags"); helpErr == nil {
		help += "\n" + text
	}
	if text, rootErr := runPinned(probe, binary, 512<<10, "--help"); rootErr == nil {
		help += "\n" + text
	}
	config := filepath.Join(work, "rclone.conf")
	argv := mounts.RuntimeQualificationArgv(config, work)
	seen := map[string]bool{}
	for _, arg := range argv {
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name := arg
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		seen[name] = true
	}
	missing := make([]string, 0)
	for flag := range seen {
		if !helpContainsFlag(help, flag) {
			missing = append(missing, flag)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("candidate does not advertise required mount flags: %s", strings.Join(missing, ", "))
	}
	return nil
}

func mountPresent(path string) bool {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer file.Close()
	needle := filepath.Clean(path)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 4 && strings.ReplaceAll(fields[4], `\040`, " ") == needle {
			return true
		}
	}
	return false
}

func allocateLoopback() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr, nil
}

func rcReady(ctx context.Context, addr, user, pass string) bool {
	client := &http.Client{Timeout: time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/core/version", strings.NewReader("{}"))
		req.SetBasicAuth(user, pass)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type smokeResult struct {
	mounted      bool
	rc           bool
	ownedProcess bool
	terminated   bool
	detail       string
}

func fuseSmoke(ctx context.Context, p paths.Paths, binary *pinnedBinary, work string) smokeResult {
	result := smokeResult{}
	if os.Geteuid() != 0 {
		result.detail = "real Android/FUSE qualification requires root"
		return result
	}
	if info, err := os.Stat(p.FuseDevice); err != nil || info.Mode()&os.ModeDevice == 0 {
		result.detail = "FUSE device is unavailable"
		return result
	}
	if _, err := provider.FindFuseHelper(p); err != nil {
		result.detail = "fusermount3 is unavailable"
		return result
	}
	source := filepath.Join(work, "source")
	mountpoint := filepath.Join(work, "mount")
	cache := filepath.Join(work, "cache")
	for _, dir := range []string{source, mountpoint, cache} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			result.detail = err.Error()
			return result
		}
	}
	if err := os.WriteFile(filepath.Join(source, "probe.txt"), []byte("nexus-runtime-qualification\n"), 0o600); err != nil {
		result.detail = err.Error()
		return result
	}
	config := filepath.Join(work, "rclone.conf")
	if err := os.WriteFile(config, []byte("[nexus_qual]\ntype = local\n"), 0o600); err != nil {
		result.detail = err.Error()
		return result
	}
	addr, err := allocateLoopback()
	if err != nil {
		result.detail = err.Error()
		return result
	}
	user, pass := "nexus-qual", "nexus-runtime-qual-secret"
	argv := mounts.RuntimeSmokeArgv(config, source, mountpoint, cache, filepath.Join(work, "mount.log"), addr, user, pass)
	cmd := binary.command(context.WithoutCancel(ctx), argv...)
	logFile, err := os.OpenFile(filepath.Join(work, "mount.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		result.detail = err.Error()
		return result
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		result.detail = err.Error()
		return result
	}
	_ = logFile.Close()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	// Hash /proc/<pid>/exe directly. Reading the symlink target and reopening
	// that pathname would reintroduce a path-swap race after exec: a replaced
	// store pathname is not necessarily the inode the child is actually using.
	processExe := fmt.Sprintf("/proc/%d/exe", cmd.Process.Pid)
	if digest, hashErr := hashFile(processExe); hashErr == nil && digest == binary.hash {
		result.ownedProcess = true
	}
	mountDeadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(mountDeadline) {
		select {
		case err := <-waitCh:
			result.detail = fmt.Sprintf("mount process exited before smoke mount: %v", err)
			return result
		default:
		}
		if mountPresent(mountpoint) {
			data, readErr := os.ReadFile(filepath.Join(mountpoint, "probe.txt"))
			if readErr == nil && string(data) == "nexus-runtime-qualification\n" {
				result.mounted = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if result.mounted {
		rcCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		result.rc = rcReady(rcCtx, addr, user, pass)
		cancel()
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-waitCh:
		result.terminated = true
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-waitCh
	}
	if mountPresent(mountpoint) {
		if helper, findErr := provider.FindFuseHelper(p); findErr == nil {
			unmountCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = exec.CommandContext(unmountCtx, helper, "-u", mountpoint).Run()
			cancel()
		}
	}
	if !result.mounted {
		result.detail = "temporary local FUSE mount did not become readable"
	} else if !result.rc {
		result.detail = "local RC endpoint did not answer core/version"
	} else if !result.ownedProcess {
		result.detail = "spawned process identity did not resolve to candidate bytes"
	} else if !result.terminated {
		result.detail = "candidate did not terminate after SIGTERM"
	} else {
		result.detail = "temporary local FUSE mount, RC, ownership and SIGTERM contract passed"
	}
	return result
}

func verifyPostQualificationHash(q *Qualification, binaryPath, expected string) bool {
	finalHash, err := hashFile(binaryPath)
	if err != nil || finalHash != expected {
		addCheck(q, "post_qualification_hash", "fail", true, "candidate bytes disappeared or changed during qualification")
		q.State = "rejected"
		return false
	}
	addCheck(q, "post_qualification_hash", "pass", true, "candidate bytes unchanged across qualification")
	return true
}

func Qualify(ctx context.Context, p paths.Paths, manifest Manifest) Qualification {
	q := Qualification{QualifierVersion: QualifierVersion, State: "rejected", StartedUnixMS: time.Now().UnixMilli(), BinarySHA256: manifest.BinarySHA256, Evidence: []string{manifestPath(p, manifest.RuntimeID), BinaryPath(p, manifest.RuntimeID)}}
	defer func() { q.FinishedUnixMS = time.Now().UnixMilli() }()
	binaryPath := BinaryPath(p, manifest.RuntimeID)
	pinned, err := openPinned(binaryPath, manifest.BinarySHA256)
	if err != nil {
		addCheck(&q, "executable", "fail", true, err.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	defer pinned.Close()
	addCheck(&q, "executable", "pass", true, "candidate is an executable regular file and bytes match manifest")

	elfMeta, err := inspectELFReader(pinned.file)
	if err != nil {
		addCheck(&q, "elf", "fail", true, err.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	addCheck(&q, "elf", "pass", true, fmt.Sprintf("%s %s %s", elfMeta.Class, elfMeta.Machine, elfMeta.OSABI))

	android, androidDetail := androidDevice()
	if archErr := architectureCompatible(android, runtime.GOARCH, elfMeta.Architecture); archErr != nil {
		addCheck(&q, "architecture", "fail", true, archErr.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	if android {
		addCheck(&q, "architecture", "pass", true, "candidate architecture="+elfMeta.Architecture+" host="+runtime.GOARCH)
	} else {
		addCheck(&q, "architecture", "pass", true, "static architecture metadata="+elfMeta.Architecture)
	}

	version, versionErr := versionCheck(ctx, pinned)
	if versionErr != nil {
		addCheck(&q, "version", "fail", true, version+" "+versionErr.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	addCheck(&q, "version", "pass", true, version)

	work, err := os.MkdirTemp(p.Normalize().RuntimeDir, ".qualify-*")
	if err != nil {
		addCheck(&q, "qualification_workspace", "fail", true, err.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	defer os.RemoveAll(work)
	_ = os.Chmod(work, 0o700)

	if err := configEnumerationCheck(ctx, pinned, work); err != nil {
		addCheck(&q, "config_remote_enumeration", "fail", true, err.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	addCheck(&q, "config_remote_enumeration", "pass", true, "candidate parsed config and enumerated nexus_qual:")

	if err := cliContractCheck(ctx, pinned, work); err != nil {
		addCheck(&q, "mount_cli_contract", "fail", true, err.Error())
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	addCheck(&q, "mount_cli_contract", "pass", true, "mount command plus generated global/command/RC flags are advertised")

	if !android {
		addCheck(&q, "android_execution", "blocked", true, androidDetail)
		addCheck(&q, "fuse_smoke_mount", "blocked", true, "requires real Android root/FUSE environment")
		addCheck(&q, "rc_runtime", "blocked", true, "requires real smoke mount")
		addCheck(&q, "process_ownership", "blocked", true, "requires real smoke mount")
		addCheck(&q, "signal_termination", "blocked", true, "requires real smoke mount")
		addCheck(&q, "diagnostics_sanitization", "pass", true, "qualification evidence is bounded and secret-pattern redacted")
		if !verifyPostQualificationHash(&q, binaryPath, manifest.BinarySHA256) {
			q.FinishedUnixMS = time.Now().UnixMilli()
			return q
		}
		q.State = "blocked_by_environment"
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	addCheck(&q, "android_execution", "pass", true, androidDetail+"; candidate version executed")

	smoke := fuseSmoke(ctx, p.Normalize(), pinned, work)
	if smoke.mounted {
		addCheck(&q, "fuse_smoke_mount", "pass", true, "temporary local FUSE mount was readable")
	} else {
		addCheck(&q, "fuse_smoke_mount", "fail", true, smoke.detail)
	}
	if smoke.rc {
		addCheck(&q, "rc_runtime", "pass", true, "authenticated loopback RC core/version succeeded")
	} else {
		addCheck(&q, "rc_runtime", "fail", true, smoke.detail)
	}
	if smoke.ownedProcess {
		addCheck(&q, "process_ownership", "pass", true, "spawned /proc executable hashes to candidate bytes")
	} else {
		addCheck(&q, "process_ownership", "fail", true, smoke.detail)
	}
	if smoke.terminated {
		addCheck(&q, "signal_termination", "pass", true, "candidate exited after SIGTERM")
	} else {
		addCheck(&q, "signal_termination", "fail", true, smoke.detail)
	}
	addCheck(&q, "diagnostics_sanitization", "pass", true, "qualification evidence is bounded and secret-pattern redacted")
	for _, check := range q.Checks {
		if check.Required && check.Status != "pass" {
			q.State = "rejected"
			q.FinishedUnixMS = time.Now().UnixMilli()
			return q
		}
	}
	if !verifyPostQualificationHash(&q, binaryPath, manifest.BinarySHA256) {
		q.FinishedUnixMS = time.Now().UnixMilli()
		return q
	}
	q.Qualified = true
	q.State = "qualified"
	q.FinishedUnixMS = time.Now().UnixMilli()
	return q
}

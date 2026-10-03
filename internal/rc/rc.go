package rc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
)

type Record struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	CreatedUnixMS int64  `json:"created_unix_ms"`
}

type Metrics struct {
	Name             string   `json:"name"`
	Available        bool     `json:"available"`
	EndpointClass    string   `json:"endpoint_class"`
	Port             int      `json:"port,omitempty"`
	TransferredBytes int64    `json:"transferred_bytes,omitempty"`
	SpeedBytesPerSec float64  `json:"speed_bytes_per_sec,omitempty"`
	Errors           int64    `json:"errors,omitempty"`
	Checks           int64    `json:"checks,omitempty"`
	Transfers        int64    `json:"transfers,omitempty"`
	OpenFiles        int64    `json:"open_files,omitempty"`
	UptimeSeconds    float64  `json:"uptime_seconds,omitempty"`
	ETASeconds       float64  `json:"eta_seconds,omitempty"`
	Unavailable      []string `json:"unavailable,omitempty"`
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}
func recordPath(p paths.Paths, name string) (string, error) {
	if !validName(name) {
		return "", fmt.Errorf("invalid rc name")
	}
	return filepath.Join(p.Normalize().RCDir, name+".json"), nil
}
func secret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func Prepare(p paths.Paths, name string) (Record, error) {
	p = p.Normalize()
	if err := p.EnsureState(); err != nil {
		return Record{}, err
	}
	var addr string
	if forced := strings.TrimSpace(os.Getenv("RNEXUS_RC_TEST_ADDR")); forced != "" {
		addr = forced
		if !loopback(addr) {
			return Record{}, fmt.Errorf("rc address must be loopback")
		}
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return Record{}, fmt.Errorf("rc port unavailable: %w", err)
		}
		addr = l.Addr().String()
		_ = l.Close()
	} else {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return Record{}, err
		}
		addr = l.Addr().String()
		_ = l.Close()
	}
	user, err := secret()
	if err != nil {
		return Record{}, err
	}
	pass, err := secret()
	if err != nil {
		return Record{}, err
	}
	rec := Record{SchemaVersion: 1, Name: name, Address: addr, Username: "nexus-" + user[:12], Password: pass, CreatedUnixMS: time.Now().UnixMilli()}
	if err := write(p, rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}
func Args(r Record) []string {
	return []string{"--rc", "--rc-addr", r.Address, "--rc-user", r.Username, "--rc-pass", r.Password}
}
func write(p paths.Paths, r Record) error {
	path, err := recordPath(p, r.Name)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rc-*")
	if err != nil {
		return err
	}
	n := tmp.Name()
	defer os.Remove(n)
	_ = tmp.Chmod(0o600)
	if _, err = tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(n, path)
}
func Read(p paths.Paths, name string) (Record, error) {
	path, err := recordPath(p, name)
	if err != nil {
		return Record{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err = json.Unmarshal(b, &r); err != nil {
		return Record{}, err
	}
	if r.SchemaVersion != 1 || r.Name != name || !loopback(r.Address) || r.Username == "" || r.Password == "" {
		return Record{}, fmt.Errorf("invalid rc record")
	}
	return r, nil
}
func Remove(p paths.Paths, name string) {
	if path, err := recordPath(p, name); err == nil {
		_ = os.Remove(path)
	}
}

func post(ctx context.Context, r Record, endpoint string) (map[string]any, error) {
	if !loopback(r.Address) {
		return nil, fmt.Errorf("rc endpoint is not loopback")
	}
	u := "http://" + r.Address + "/" + strings.TrimPrefix(endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(r.Username, r.Password)
	cl := &http.Client{Timeout: 900 * time.Millisecond}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rc %s returned %s", endpoint, resp.Status)
	}
	var v map[string]any
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	if err = json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return v, nil
}
func num(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		n, e := x.Float64()
		return n, e == nil
	case string:
		n, e := strconv.ParseFloat(x, 64)
		return n, e == nil
	}
	return 0, false
}
func MetricsFor(ctx context.Context, p paths.Paths, name string) (Metrics, error) {
	r, err := Read(p, name)
	if err != nil {
		return Metrics{Name: name, EndpointClass: "loopback", Unavailable: []string{"rc_record"}}, err
	}
	_, portText, _ := net.SplitHostPort(r.Address)
	port, _ := strconv.Atoi(portText)
	out := Metrics{Name: name, EndpointClass: "loopback", Port: port}
	stats, err := post(ctx, r, "core/stats")
	if err != nil {
		out.Unavailable = []string{"core/stats"}
		return out, err
	}
	out.Available = true
	if n, ok := num(stats, "bytes"); ok {
		out.TransferredBytes = int64(n)
	}
	if n, ok := num(stats, "speed"); ok {
		out.SpeedBytesPerSec = n
	}
	if n, ok := num(stats, "errors"); ok {
		out.Errors = int64(n)
	}
	if n, ok := num(stats, "checks"); ok {
		out.Checks = int64(n)
	}
	if n, ok := num(stats, "transfers"); ok {
		out.Transfers = int64(n)
	}
	if n, ok := num(stats, "elapsedTime"); ok {
		out.UptimeSeconds = n
	}
	if n, ok := num(stats, "eta"); ok {
		out.ETASeconds = n
	}
	vfs, e := post(ctx, r, "vfs/stats")
	if e != nil {
		out.Unavailable = append(out.Unavailable, "vfs/stats")
	} else {
		for _, k := range []string{"openFiles", "inUse", "open_files"} {
			if n, ok := num(vfs, k); ok {
				out.OpenFiles = int64(n)
				break
			}
		}
	}
	return out, nil
}

package rc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"strings"
	"testing"
	"time"
)

func tp(t *testing.T) paths.Paths {
	d := t.TempDir()
	p := paths.Paths{StateDir: d, RunDir: filepath.Join(d, "run")}.Normalize()
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPrepareLoopbackAndCredentialsStayPrivate(t *testing.T) {
	p := tp(t)
	r, err := Prepare(p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !loopback(r.Address) || r.Username == "" || r.Password == "" {
		t.Fatal(r)
	}
	b, _ := json.Marshal(Metrics{Name: "drive"})
	if string(b) == r.Password || string(b) == r.Username {
		t.Fatal("credential leaked")
	}
	info, err := os.Stat(filepath.Join(p.RCDir, "drive.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
}
func TestPrepareRejectsNonLoopbackAndCollision(t *testing.T) {
	p := tp(t)
	t.Setenv("RNEXUS_RC_TEST_ADDR", "0.0.0.0:0")
	if _, err := Prepare(p, "x"); err == nil {
		t.Fatal("expected reject")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("RNEXUS_RC_TEST_ADDR", ln.Addr().String())
	if _, err := Prepare(p, "x"); err == nil {
		t.Fatal("expected collision")
	}
}
func TestMetricsParsingAndAuth(t *testing.T) {
	p := tp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/core/stats", func(w http.ResponseWriter, r *http.Request) {
		u, pw, ok := r.BasicAuth()
		if !ok || u != "u" || pw != "p" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"bytes":123,"speed":4.5,"errors":2,"checks":3,"transfers":1,"elapsedTime":9,"eta":7}`))
	})
	mux.HandleFunc("/vfs/stats", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"openFiles":6}`)) })
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	rec := Record{SchemaVersion: 1, Name: "drive", Address: ln.Addr().String(), Username: "u", Password: "p", CreatedUnixMS: time.Now().UnixMilli()}
	if err := write(p, rec); err != nil {
		t.Fatal(err)
	}
	m, err := MetricsFor(context.Background(), p, "drive")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Available || m.TransferredBytes != 123 || m.OpenFiles != 6 {
		t.Fatal(m)
	}
}

func TestMetricsAuthFailureDegradesWithoutCredentialEcho(t *testing.T) {
	p := tp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/core/stats", func(w http.ResponseWriter, r *http.Request) {
		_, pw, _ := r.BasicAuth()
		if pw != "expected" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	rec := Record{SchemaVersion: 1, Name: "drive", Address: ln.Addr().String(), Username: "u", Password: "wrong", CreatedUnixMS: time.Now().UnixMilli()}
	if err := write(p, rec); err != nil {
		t.Fatal(err)
	}
	m, err := MetricsFor(context.Background(), p, "drive")
	if err == nil || m.Available {
		t.Fatalf("expected graceful auth failure: m=%+v err=%v", m, err)
	}
	if strings.Contains(err.Error(), "wrong") || strings.Contains(err.Error(), "Basic ") {
		t.Fatalf("credential leaked in error: %v", err)
	}
}

func TestArgsUseOnlySupportedRCFlags(t *testing.T) {
	r := Record{Address: "127.0.0.1:12345", Username: "user", Password: "pass"}
	args := Args(r)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--rc-no-open-browser") {
		t.Fatalf("obsolete rclone flag leaked into argv: %v", args)
	}
	for _, want := range []string{"--rc", "--rc-addr", r.Address, "--rc-user", r.Username, "--rc-pass", r.Password} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in argv %v", want, args)
		}
	}
}

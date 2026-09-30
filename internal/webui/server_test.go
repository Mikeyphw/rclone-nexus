package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

type testWeb struct {
	server *Server
	info   Info
	client *http.Client
	base   *url.URL
	cancel context.CancelFunc
	paths  paths.Paths
}

func newTestWeb(t *testing.T, idle time.Duration) testWeb {
	t.Helper()
	t.Setenv("RNEXUS_RACD_DISABLE", "1")
	root := t.TempDir()
	static := filepath.Join(root, "module", "webroot")
	if err := os.MkdirAll(static, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(static, "index.html"), []byte("<!doctype html><title>Nexus</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{StateDir: filepath.Join(root, "state"), ModuleDir: filepath.Join(root, "module"), ProviderModuleDir: filepath.Join(root, "provider"), RcloneConfig: filepath.Join(root, "provider", "conf", "rclone.conf"), FuseDevice: filepath.Join(root, "fuse")}.Normalize()
	engine := control.New(p)
	server, err := New(Config{Paths: p, Engine: engine, StaticDir: static, IdleTimeout: idle, SessionTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	info, err := server.Start(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	base, _ := url.Parse(info.URL)
	return testWeb{server: server, info: info, client: client, base: base, cancel: cancel, paths: p}
}

func (tw testWeb) close() {
	tw.cancel()
	_ = tw.server.Close()
}

func bootstrapClient(t *testing.T, tw testWeb, bootstrapURL string) string {
	t.Helper()
	response, err := tw.client.Get(bootstrapURL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap status=%d", response.StatusCode)
	}
	csrf := ""
	for _, cookie := range tw.client.Jar.Cookies(tw.base) {
		if cookie.Name == CSRFCookie {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("csrf cookie missing")
	}
	return csrf
}

func postJSON(t *testing.T, tw testWeb, path, csrf, origin string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, tw.info.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Rclone-Nexus-CSRF", csrf)
	req.Header.Set("Origin", origin)
	response, err := tw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestStandaloneBootstrapSecurityAndTypedRoute(t *testing.T) {
	tw := newTestWeb(t, 5*time.Second)
	defer tw.close()
	csrf := bootstrapClient(t, tw, tw.info.BootstrapURL)

	replay, err := tw.client.Get(tw.info.BootstrapURL)
	if err != nil {
		t.Fatal(err)
	}
	replay.Body.Close()
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bootstrap replay status=%d", replay.StatusCode)
	}

	caps, err := tw.client.Get(tw.info.URL + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	if caps.StatusCode != http.StatusOK {
		t.Fatalf("capabilities status=%d", caps.StatusCode)
	}
	caps.Body.Close()

	good := postJSON(t, tw, "/api/v1/query/provider.status", csrf, tw.info.URL, `{}`)
	if good.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(good.Body)
		good.Body.Close()
		t.Fatalf("typed query status=%d body=%s", good.StatusCode, data)
	}
	var envelope Envelope
	if err := json.NewDecoder(good.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	good.Body.Close()
	if envelope.SchemaVersion != 1 || envelope.Response.RequestID == "" {
		t.Fatalf("bad envelope: %+v", envelope)
	}

	wrongClass := postJSON(t, tw, "/api/v1/run/provider.status", csrf, tw.info.URL, `{}`)
	wrongClass.Body.Close()
	if wrongClass.StatusCode != http.StatusNotFound {
		t.Fatalf("class mismatch status=%d", wrongClass.StatusCode)
	}

	wrongOrigin := postJSON(t, tw, "/api/v1/query/provider.status", csrf, "http://127.0.0.1:1", `{}`)
	wrongOrigin.Body.Close()
	if wrongOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("origin status=%d", wrongOrigin.StatusCode)
	}

	wrongCSRF := postJSON(t, tw, "/api/v1/query/provider.status", "wrong", tw.info.URL, `{}`)
	wrongCSRF.Body.Close()
	if wrongCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf status=%d", wrongCSRF.StatusCode)
	}
}

func TestHostBoundsAndRuntimeStatePermissions(t *testing.T) {
	tw := newTestWeb(t, 5*time.Second)
	defer tw.close()
	csrf := bootstrapClient(t, tw, tw.info.BootstrapURL)

	req, _ := http.NewRequest(http.MethodGet, tw.info.URL+"/api/v1/transport", nil)
	req.Host = "evil.example"
	response, err := tw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("host status=%d", response.StatusCode)
	}

	huge := `{"x":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	tooLarge := postJSON(t, tw, "/api/v1/query/provider.status", csrf, tw.info.URL, huge)
	tooLarge.Body.Close()
	if tooLarge.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status=%d", tooLarge.StatusCode)
	}

	info, err := os.Stat(filepath.Join(tw.paths.RunDir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("runtime state mode=%#o", info.Mode().Perm())
	}
}

func TestReuseIssuesFreshOneUseBootstrap(t *testing.T) {
	tw := newTestWeb(t, 5*time.Second)
	defer tw.close()
	first := bootstrapClient(t, tw, tw.info.BootstrapURL)
	if first == "" {
		t.Fatal("first csrf missing")
	}
	info, err := IssueFromExisting(context.Background(), tw.paths)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != os.Getpid() || info.BootstrapURL == tw.info.BootstrapURL {
		t.Fatalf("unexpected reuse info: %+v", info)
	}
	bootstrapClient(t, tw, info.BootstrapURL)
}

func TestIdleShutdownRemovesOwnedState(t *testing.T) {
	tw := newTestWeb(t, 100*time.Millisecond)
	defer tw.cancel()
	select {
	case <-tw.server.done:
	case <-time.After(2 * time.Second):
		t.Fatal("idle shutdown did not complete")
	}
	if _, err := os.Stat(filepath.Join(tw.paths.RunDir, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("runtime state survived idle shutdown: %v", err)
	}
}

func TestEmbeddedBridgeRejectsUnknownAndClassMismatch(t *testing.T) {
	t.Setenv("RNEXUS_RACD_DISABLE", "1")
	root := t.TempDir()
	p := paths.Paths{StateDir: filepath.Join(root, "state"), ModuleDir: filepath.Join(root, "module")}.Normalize()
	engine := control.New(p)
	unknown := protocol.NewRequest("webui-test-1", "shell.exec", protocol.ClassQuery, map[string]any{})
	encoded, _ := EncodeRequest(unknown)
	if _, err := Bridge(context.Background(), p, engine, encoded); err == nil {
		t.Fatal("unknown operation accepted")
	}
	mismatch := protocol.NewRequest("webui-test-2", "provider.status", protocol.ClassRun, map[string]any{})
	encoded, _ = EncodeRequest(mismatch)
	if _, err := Bridge(context.Background(), p, engine, encoded); err == nil {
		t.Fatal("class mismatch accepted")
	}
}

func TestOpenAndroidGeneratesFixedVIEWAction(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "args.txt")
	fake := filepath.Join(root, "am")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$RNEXUS_AM_CAPTURE\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_ANDROID_AM", fake)
	t.Setenv("RNEXUS_AM_CAPTURE", output)
	url := "http://127.0.0.1:12345/bootstrap?token=abc_DEF-123"
	if err := OpenAndroid(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"start", "-a", "android.intent.action.VIEW", "-d", url}
	if len(got) != len(want) {
		t.Fatalf("VIEW args=%q", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("VIEW arg %d=%q want=%q", index, got[index], want[index])
		}
	}
	if err := OpenAndroid(context.Background(), "https://example.com/"); err == nil {
		t.Fatal("non-loopback URL accepted")
	}
}

func TestTypedRouteAcceptsBoundedClientRequestID(t *testing.T) {
	tw := newTestWeb(t, 5*time.Second)
	defer tw.close()
	csrf := bootstrapClient(t, tw, tw.info.BootstrapURL)
	req, err := http.NewRequest(http.MethodPost, tw.info.URL+"/api/v1/query/provider.status", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Rclone-Nexus-CSRF", csrf)
	req.Header.Set("X-Rclone-Nexus-Request-ID", "webui-known-request")
	req.Header.Set("Origin", tw.info.URL)
	response, err := tw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Response.RequestID != "webui-known-request" {
		t.Fatalf("request id=%q", envelope.Response.RequestID)
	}

	bad, _ := http.NewRequest(http.MethodPost, tw.info.URL+"/api/v1/query/provider.status", strings.NewReader(`{}`))
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set("X-Rclone-Nexus-CSRF", csrf)
	bad.Header.Set("X-Rclone-Nexus-Request-ID", "bad request/id")
	bad.Header.Set("Origin", tw.info.URL)
	badResponse, err := tw.client.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	badResponse.Body.Close()
	if badResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid request id status=%d", badResponse.StatusCode)
	}
}

package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"rclone-nexus/internal/control"
	"rclone-nexus/internal/daemon"
	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/protocol"
)

const (
	SessionCookie  = "rnexus_webui"
	CSRFCookie     = "rnexus_csrf"
	stateFileName  = "webui-server.json"
	defaultIdle    = 10 * time.Minute
	defaultTTL     = 30 * time.Minute
	bootstrapTTL   = 2 * time.Minute
	maxBodyBytes   = 64 << 10
	maxHeaderBytes = 16 << 10
)

type Config struct {
	Paths       paths.Paths
	Engine      *control.Engine
	StaticDir   string
	IdleTimeout time.Duration
	SessionTTL  time.Duration
	Logf        func(string, ...any)
}

type Info struct {
	SchemaVersion int    `json:"schema_version"`
	Transport     string `json:"transport"`
	URL           string `json:"url"`
	BootstrapURL  string `json:"bootstrap_url"`
	PID           int    `json:"pid"`
}

type runtimeState struct {
	SchemaVersion int    `json:"schema_version"`
	PID           int    `json:"pid"`
	URL           string `json:"url"`
	AdminSecret   string `json:"admin_secret"`
	StartedUnixMS int64  `json:"started_unix_ms"`
}

type bootstrap struct {
	token   string
	expires time.Time
}

type session struct {
	expires time.Time
	csrf    string
}

type Server struct {
	cfg Config

	mu         sync.Mutex
	sessions   map[string]session
	bootstraps map[string]bootstrap
	admin      string
	last       time.Time
	active     int
	listener   net.Listener
	http       *http.Server
	done       chan struct{}
	doneOnce   sync.Once
	origin     string
}

func secret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func New(cfg Config) (*Server, error) {
	if cfg.Engine == nil {
		return nil, errors.New("webui engine is required")
	}
	cfg.Paths = cfg.Paths.Normalize()
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = defaultIdle
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultTTL
	}
	admin, err := secret()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, sessions: map[string]session{}, bootstraps: map[string]bootstrap{},
		admin: admin, last: time.Now(), done: make(chan struct{}),
	}, nil
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

func (s *Server) statePath() string {
	return filepath.Join(s.cfg.Paths.RunDir, stateFileName)
}

func writePrivateJSON(path string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(tmp, 0, 0); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Server) IssueBootstrap() (Info, error) {
	token, err := secret()
	if err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	s.bootstraps[token] = bootstrap{token: token, expires: time.Now().Add(bootstrapTTL)}
	origin := s.origin
	s.last = time.Now()
	s.mu.Unlock()
	if origin == "" {
		return Info{}, errors.New("webui server is not listening")
	}
	return Info{SchemaVersion: 1, Transport: "standalone", URL: origin, BootstrapURL: origin + "/bootstrap?token=" + token, PID: os.Getpid()}, nil
}

func (s *Server) Start(ctx context.Context) (Info, error) {
	if err := s.cfg.Paths.EnsureState(); err != nil {
		return Info{}, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Info{}, err
	}
	s.listener = ln
	s.origin = "http://" + ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/bootstrap", s.bootstrapHandler)
	mux.HandleFunc("/__nexus/bootstrap", s.adminBootstrapHandler)
	mux.Handle("/api/v1/", s.apiHandler())
	mux.Handle("/", s.staticHandler())

	s.http = &http.Server{
		Handler: s.security(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: maxHeaderBytes,
	}
	state := runtimeState{SchemaVersion: 1, PID: os.Getpid(), URL: s.origin, AdminSecret: s.admin, StartedUnixMS: time.Now().UnixMilli()}
	if err := writePrivateJSON(s.statePath(), state); err != nil {
		_ = ln.Close()
		return Info{}, err
	}
	info, err := s.IssueBootstrap()
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(s.statePath())
		return Info{}, err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
	}()
	go s.idleWatch(ctx)
	go func() {
		err := s.http.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logf("webui serve error: %v", err)
		}
		s.cleanupState()
		s.doneOnce.Do(func() { close(s.done) })
	}()
	return info, nil
}

func (s *Server) Wait() { <-s.done }

func (s *Server) Close() error {
	if s.http == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.http.Shutdown(ctx)
	s.cleanupState()
	return err
}

func (s *Server) cleanupState() {
	payload, err := os.ReadFile(s.statePath())
	if err != nil {
		return
	}
	var state runtimeState
	if json.Unmarshal(payload, &state) == nil && state.PID == os.Getpid() {
		_ = os.Remove(s.statePath())
	}
}

func (s *Server) idleWatch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			idle := time.Since(s.last) >= s.cfg.IdleTimeout && s.active == 0
			s.mu.Unlock()
			if idle {
				_ = s.Close()
				return
			}
		}
	}
}

func (s *Server) touch() {
	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != strings.TrimPrefix(s.origin, "http://") {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.ContentLength > maxBodyBytes {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		s.touch()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) consumeBootstrap(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.bootstraps[token]
	if !ok || item.expires.Before(time.Now()) || subtle.ConstantTimeCompare([]byte(token), []byte(item.token)) != 1 {
		return false
	}
	delete(s.bootstraps, token)
	return true
}

func (s *Server) bootstrapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if !s.consumeBootstrap(r.URL.Query().Get("token")) {
		http.Error(w, "invalid or replayed bootstrap token", http.StatusUnauthorized)
		return
	}
	sid, err := secret()
	if err != nil {
		http.Error(w, "entropy failure", http.StatusInternalServerError)
		return
	}
	csrf, err := secret()
	if err != nil {
		http.Error(w, "entropy failure", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	s.sessions[sid] = session{expires: time.Now().Add(s.cfg.SessionTTL), csrf: csrf}
	s.mu.Unlock()
	maxAge := int(s.cfg.SessionTTL.Seconds())
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: csrf, Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/"><title>Rclone Nexus</title></head><body>Authentication complete. <a href="/">Continue</a>.</body></html>`)
}

func (s *Server) adminBootstrapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Rclone-Nexus-Admin")), []byte(s.admin)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	info, err := s.IssueBootstrap()
	if err != nil {
		http.Error(w, "bootstrap unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSONBounded(w, http.StatusOK, info)
}

func (s *Server) authenticated(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || cookie.Value == "" {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[cookie.Value]
	if !ok || sess.expires.Before(time.Now()) {
		delete(s.sessions, cookie.Value)
		return session{}, false
	}
	return sess, true
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.authenticated(r)
		if !ok {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("Origin") != s.origin {
				http.Error(w, "origin rejected", http.StatusForbidden)
				return
			}
			csrfCookie, err := r.Cookie(CSRFCookie)
			if err != nil || csrfCookie.Value == "" || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(sess.csrf)) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Rclone-Nexus-CSRF")), []byte(sess.csrf)) != 1 {
				http.Error(w, "csrf rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) staticHandler() http.Handler {
	if strings.TrimSpace(s.cfg.StaticDir) == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	}
	files := http.FileServer(http.Dir(s.cfg.StaticDir))
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if strings.Contains(r.URL.Path, "..") || strings.Contains(r.URL.Path, "\\") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		files.ServeHTTP(w, r)
	}))
}

func writeJSONBounded(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "encode failure", http.StatusInternalServerError)
		return
	}
	if len(payload) > protocol.MaxResponseBytes {
		http.Error(w, "response too large", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n"))
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	writeJSONBounded(w, http.StatusOK, map[string]any{"schema_version": 1, "ok": true, "transport": "standalone"})
}

func decodeArgs(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	if r.Body == nil {
		return json.RawMessage(`{}`), true
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return nil, false
	}
	if len(data) > maxBodyBytes {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	var object map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&object); err != nil {
		http.Error(w, "body must be one JSON object", http.StatusBadRequest)
		return nil, false
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return nil, false
	}
	return normalized, true
}

func classForPath(path string) (string, string, bool) {
	prefixes := []struct{ prefix, class string }{
		{"/api/v1/query/", protocol.ClassQuery},
		{"/api/v1/preview/", protocol.ClassPreview},
		{"/api/v1/run/", protocol.ClassRun},
		{"/api/v1/reconcile/", protocol.ClassReconcile},
		{"/api/v1/cancel/", protocol.ClassCancel},
	}
	for _, item := range prefixes {
		if strings.HasPrefix(path, item.prefix) {
			name := strings.TrimPrefix(path, item.prefix)
			if name != "" && !strings.Contains(name, "/") {
				return name, item.class, true
			}
		}
	}
	return "", "", false
}

func (s *Server) apiHandler() http.Handler {
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/health":
			s.healthHandler(w, r)
			return
		case "/api/v1/transport":
			if r.Method != http.MethodGet {
				http.Error(w, "method", http.StatusMethodNotAllowed)
				return
			}
			writeJSONBounded(w, http.StatusOK, map[string]any{"schema_version": 1, "transport": "standalone", "typed_backend": true})
			return
		case "/api/v1/capabilities":
			if r.Method != http.MethodGet {
				http.Error(w, "method", http.StatusMethodNotAllowed)
				return
			}
			writeJSONBounded(w, http.StatusOK, s.cfg.Engine.Capabilities())
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		name, class, ok := classForPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if descriptor, found := s.cfg.Engine.Descriptor(name); !found || descriptor.Class != class {
			http.Error(w, "operation is not allow-listed for this route", http.StatusNotFound)
			return
		}
		rawArgs, ok := decodeArgs(w, r)
		if !ok {
			return
		}
		var args any
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			http.Error(w, "invalid args", http.StatusBadRequest)
			return
		}
		request := protocol.NewRequest(webRequestID(), name, class, args)
		result := daemon.Execute(r.Context(), s.cfg.Paths, s.cfg.Engine, request)
		writeJSONBounded(w, http.StatusOK, Envelope{SchemaVersion: 1, Response: result.Response, Events: result.Events})
	}))
}

func webRequestID() string {
	token, err := secret()
	if err != nil {
		return "webui-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if len(token) > 24 {
		token = token[:24]
	}
	return "webui-" + token
}

type Envelope struct {
	SchemaVersion int               `json:"schema_version"`
	Response      protocol.Response `json:"response"`
	Events        []protocol.Event  `json:"events,omitempty"`
}

// IssueFromExisting returns a fresh one-use bootstrap URL for a live owned
// standalone server. It never exposes the root-only administrative secret.
func IssueFromExisting(ctx context.Context, p paths.Paths) (Info, error) {
	p = p.Normalize()
	payload, err := os.ReadFile(filepath.Join(p.RunDir, stateFileName))
	if err != nil {
		return Info{}, err
	}
	var state runtimeState
	if err := json.Unmarshal(payload, &state); err != nil {
		return Info{}, err
	}
	if state.PID <= 1 || state.URL == "" || state.AdminSecret == "" || !processAlive(state.PID) {
		return Info{}, errors.New("webui runtime state is stale")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, state.URL+"/__nexus/bootstrap", strings.NewReader("{}"))
	if err != nil {
		return Info{}, err
	}
	req.Host = strings.TrimPrefix(state.URL, "http://")
	req.Header.Set("X-Rclone-Nexus-Admin", state.AdminSecret)
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("existing webui rejected bootstrap request: %s", resp.Status)
	}
	var info Info
	dec := json.NewDecoder(io.LimitReader(resp.Body, 32<<10))
	if err := dec.Decode(&info); err != nil {
		return Info{}, err
	}
	return info, nil
}

func processAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

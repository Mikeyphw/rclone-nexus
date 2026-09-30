package previewproof

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"rclone-nexus/internal/paths"
)

const (
	SchemaVersion = 1
	defaultTTL    = 2 * time.Minute
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

type Proof struct {
	SchemaVersion int    `json:"schema_version"`
	Token         string `json:"token"`
	Resource      string `json:"resource"`
	Revision      uint64 `json:"revision"`
	Digest        string `json:"digest"`
	IssuedUnixMS  int64  `json:"issued_unix_ms"`
	ExpiresUnixMS int64  `json:"expires_unix_ms"`
}

type Error struct {
	Code   string
	Detail string
}

func (e Error) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}
func Code(err error) string {
	var target Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func dir(p paths.Paths) string { return filepath.Join(p.Normalize().RunDir, "previews") }
func pathFor(p paths.Paths, token string) (string, error) {
	if !tokenPattern.MatchString(token) {
		return "", Error{Code: "preview_required", Detail: "preview token is missing or invalid"}
	}
	return filepath.Join(dir(p), token+".json"), nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func cleanupExpired(p paths.Paths, now time.Time) {
	entries, err := os.ReadDir(dir(p))
	if err != nil {
		return
	}
	checked := 0
	for _, entry := range entries {
		if checked >= 256 {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		checked++
		path := filepath.Join(dir(p), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var proof Proof
		if json.Unmarshal(data, &proof) != nil || proof.ExpiresUnixMS <= now.UnixMilli() {
			_ = os.Remove(path)
		}
	}
}

func Issue(p paths.Paths, resource string, revision uint64, digest string) (Proof, error) {
	p = p.Normalize()
	if resource == "" || digest == "" {
		return Proof{}, fmt.Errorf("preview proof resource/digest required")
	}
	if err := os.MkdirAll(dir(p), 0o700); err != nil {
		return Proof{}, err
	}
	if err := os.Chmod(dir(p), 0o700); err != nil {
		return Proof{}, err
	}
	now := time.Now()
	for attempt := 0; attempt < 4; attempt++ {
		token, err := randomToken()
		if err != nil {
			return Proof{}, err
		}
		proof := Proof{SchemaVersion: SchemaVersion, Token: token, Resource: resource, Revision: revision, Digest: digest, IssuedUnixMS: now.UnixMilli(), ExpiresUnixMS: now.Add(defaultTTL).UnixMilli()}
		payload, err := json.Marshal(proof)
		if err != nil {
			return Proof{}, err
		}
		path, _ := pathFor(p, token)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return Proof{}, err
		}
		writeErr := func() error {
			defer f.Close()
			if err := f.Chmod(0o600); err != nil {
				return err
			}
			if os.Geteuid() == 0 {
				if err := f.Chown(0, 0); err != nil {
					return err
				}
			}
			if _, err := f.Write(append(payload, '\n')); err != nil {
				return err
			}
			return f.Sync()
		}()
		if writeErr != nil {
			_ = os.Remove(path)
			return Proof{}, writeErr
		}
		return proof, nil
	}
	return Proof{}, fmt.Errorf("preview token collision budget exhausted")
}

func Consume(p paths.Paths, token, resource string, revision uint64, digest string) error {
	path, err := pathFor(p, token)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Error{Code: "preview_required", Detail: "preview token was not found or was already consumed"}
		}
		return err
	}
	// One use even if the proof is stale/mismatched: the caller must preview again.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	var proof Proof
	if err := json.Unmarshal(data, &proof); err != nil || proof.SchemaVersion != SchemaVersion {
		return Error{Code: "preview_invalid", Detail: "preview proof is malformed"}
	}
	if time.Now().UnixMilli() > proof.ExpiresUnixMS {
		return Error{Code: "preview_expired", Detail: "preview proof expired"}
	}
	if proof.Resource != resource || proof.Revision != revision || proof.Digest != digest {
		return Error{Code: "preview_mismatch", Detail: "preview proof does not match the candidate/revision"}
	}
	return nil
}

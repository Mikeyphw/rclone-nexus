package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rclone-nexus/internal/paths"
)

func TestRemoteNamesAndBrowseAreCredentialFree(t *testing.T) {
	root := t.TempDir()
	p := paths.Paths{ModuleDir: filepath.Join(root, "module"), StateDir: filepath.Join(root, "state")}.Normalize()
	bin := filepath.Join(p.ModuleDir, "system", "bin", "rclone")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = listremotes ]; then printf 'drive:\\nother:\\n'; exit 0; fi\nif [ \"$1\" = lsjson ]; then printf '[{\"Name\":\"Folder\",\"Path\":\"Folder\",\"IsDir\":true},{\"Name\":\"file.txt\",\"Path\":\"file.txt\",\"Size\":7}]'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ManagedRcloneConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ManagedRcloneConfig, []byte("[drive]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := ListRemotes(context.Background(), p)
	if err != nil || len(names) != 2 {
		t.Fatalf("%v %v", names, err)
	}
	r, err := Browse(context.Background(), p, "drive", "", 10)
	if err != nil || len(r.Entries) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := Browse(context.Background(), p, "drive", "backend,opt=secret:", 10); err == nil {
		t.Fatal("on-the-fly syntax accepted")
	}
}

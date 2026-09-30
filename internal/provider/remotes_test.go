package provider

import (
	"context"
	"os"
	"path/filepath"
	"rclone-nexus/internal/paths"
	"testing"
)

func TestRemoteNamesAndBrowseAreCredentialFree(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "rclone")
	script := "#!/bin/sh\nif [ \"$1\" = listremotes ]; then printf 'drive:\\nother:\\n'; exit 0; fi\nif [ \"$1\" = lsjson ]; then printf '[{\"Name\":\"Folder\",\"Path\":\"Folder\",\"IsDir\":true},{\"Name\":\"file.txt\",\"Path\":\"file.txt\",\"Size\":7}]'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_RCLONE_BIN", bin)
	p := paths.Paths{StateDir: root, RcloneConfig: filepath.Join(root, "rclone.conf")}.Normalize()
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

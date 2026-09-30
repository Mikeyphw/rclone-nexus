package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderModuleVersionReadsOnlyModuleMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "module.prop"), []byte("id=rclone\nversion=v1.75.1-provider\nversionCode=17501\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := providerModuleVersion(dir); got != "v1.75.1-provider" {
		t.Fatalf("version=%q", got)
	}
}

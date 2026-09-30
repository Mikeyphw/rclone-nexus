package rootmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFixtures(t *testing.T) {
	cases := []struct {
		name string
		dirs []string
		hint string
		kind string
		web  bool
	}{
		{name: "magisk", dirs: []string{"magisk", "modules"}, kind: KindMagisk},
		{name: "ksu", dirs: []string{"ksu", "modules"}, kind: KindKernelSU, web: true},
		{name: "ksu-next", dirs: []string{"ksu", "modules"}, hint: KindKernelSUNext, kind: KindKernelSUNext, web: true},
		{name: "apatch", dirs: []string{"ap", "modules"}, kind: KindAPatch, web: true},
		{name: "compatible", dirs: []string{"modules"}, kind: KindCompatible},
		{name: "unknown", kind: KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			for _, rel := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(base, rel), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("RNEXUS_ADB_DIR", base)
			t.Setenv("RNEXUS_ROOT_MANAGER_HINT", tc.hint)
			got := Detect()
			if got.Kind != tc.kind {
				t.Fatalf("kind=%s want=%s status=%+v", got.Kind, tc.kind, got)
			}
			if got.Capabilities.EmbeddedWebUI != tc.web {
				t.Fatalf("webui=%v want=%v", got.Capabilities.EmbeddedWebUI, tc.web)
			}
			if tc.kind != KindUnknown && !got.Compatible {
				t.Fatalf("expected compatible: %+v", got)
			}
		})
	}
}

func TestUnknownCompatibleDoesNotInventEmbeddedWebUI(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_ADB_DIR", base)
	got := Detect()
	if got.Kind != KindCompatible || got.Capabilities.EmbeddedWebUI {
		t.Fatalf("unexpected status: %+v", got)
	}
}

func TestUnknownCompatibleCapabilitiesAreConservative(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RNEXUS_ADB_DIR", base)
	got := Detect()
	if got.Kind != KindCompatible || !got.Capabilities.ModuleHooks {
		t.Fatalf("unexpected compatible status: %+v", got)
	}
	if got.Capabilities.ServiceHook || got.Capabilities.PostFSDataHook || got.Capabilities.UninstallHook || got.Capabilities.ActionHook || got.Capabilities.EmbeddedWebUI {
		t.Fatalf("unproven capability advertised: %+v", got.Capabilities)
	}
}

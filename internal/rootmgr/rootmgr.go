package rootmgr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	KindMagisk       = "magisk"
	KindKernelSU     = "kernelsu"
	KindKernelSUNext = "kernelsu-next"
	KindAPatch       = "apatch"
	KindCompatible   = "compatible"
	KindUnknown      = "unknown"
)

type Capabilities struct {
	ModuleHooks    bool `json:"module_hooks"`
	ServiceHook    bool `json:"service_hook"`
	PostFSDataHook bool `json:"post_fs_data_hook"`
	UninstallHook  bool `json:"uninstall_hook"`
	ActionHook     bool `json:"action_hook"`
	EmbeddedWebUI  bool `json:"embedded_webui"`
	UpdateStaging  bool `json:"update_staging"`
}

type Status struct {
	SchemaVersion int          `json:"schema_version"`
	Kind          string       `json:"kind"`
	Name          string       `json:"name"`
	Version       string       `json:"version,omitempty"`
	Compatible    bool         `json:"compatible"`
	Capabilities  Capabilities `json:"capabilities"`
	Evidence      []string     `json:"evidence,omitempty"`
}

func adbDir() string {
	if v := os.Getenv("RNEXUS_ADB_DIR"); v != "" {
		return filepath.Clean(v)
	}
	return "/data/adb"
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func forcedKind() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RNEXUS_ROOT_MANAGER_HINT"))) {
	case KindMagisk:
		return KindMagisk
	case KindKernelSU, "ksu":
		return KindKernelSU
	case KindKernelSUNext, "ksu-next", "ksunext":
		return KindKernelSUNext
	case KindAPatch, "ap":
		return KindAPatch
	case KindCompatible:
		return KindCompatible
	case KindUnknown:
		return KindUnknown
	default:
		return ""
	}
}

func managerVersion(kind string) string {
	// Root-manager CLIs are not guaranteed to be present in the caller's PATH.
	// Final qualification commonly invokes racctl from Termux while the manager
	// binary lives under the manager-owned /data/adb state tree. Probe canonical
	// absolute locations first, then PATH aliases for compatibility.
	adb := adbDir()
	commands := map[string][][]string{
		KindMagisk: {
			{filepath.Join(adb, "magisk", "magisk"), "-v"}, {"magisk", "-v"}, {"magisk", "--version"},
		},
		KindKernelSU: {
			{filepath.Join(adb, "ksud"), "--version"}, {filepath.Join(adb, "ksu", "bin", "ksud"), "--version"}, {"ksud", "--version"}, {"ksu", "--version"},
		},
		KindKernelSUNext: {
			{filepath.Join(adb, "ksud"), "--version"}, {filepath.Join(adb, "ksu", "bin", "ksud-next"), "--version"}, {filepath.Join(adb, "ksu", "bin", "ksud"), "--version"}, {"ksud-next", "--version"}, {"ksud", "--version"},
		},
		KindAPatch: {
			{filepath.Join(adb, "apd"), "--version"}, {filepath.Join(adb, "ap", "bin", "apd"), "--version"}, {"apd", "--version"}, {"apd", "-V"},
		},
	}
	for _, argv := range commands[kind] {
		binary := argv[0]
		if strings.ContainsRune(binary, os.PathSeparator) {
			info, err := os.Stat(binary)
			if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
				continue
			}
		} else if _, err := exec.LookPath(binary); err != nil {
			continue
		}
		cmd := exec.Command(binary, argv[1:]...)
		out, err := cmd.Output()
		if err == nil {
			line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
			if line != "" {
				return line
			}
		}
	}
	return ""
}

func Detect() Status {
	adb := adbDir()
	kind := forcedKind()
	evidence := []string{}
	if kind != "" {
		evidence = append(evidence, "explicit_hint")
	}
	if kind == "" {
		switch {
		case dir(filepath.Join(adb, "ksu")) && (exists(filepath.Join(adb, "ksu", ".ksu_next")) || commandExists("ksud-next")):
			kind = KindKernelSUNext
			evidence = append(evidence, "kernelsu_state", "next_marker")
		case dir(filepath.Join(adb, "ksu")) || commandExists("ksud"):
			kind = KindKernelSU
			evidence = append(evidence, "kernelsu_state")
		case dir(filepath.Join(adb, "ap")) || dir(filepath.Join(adb, "apatch")) || commandExists("apd"):
			kind = KindAPatch
			evidence = append(evidence, "apatch_state")
		case dir(filepath.Join(adb, "magisk")) || commandExists("magisk"):
			kind = KindMagisk
			evidence = append(evidence, "magisk_state")
		case dir(filepath.Join(adb, "modules")):
			kind = KindCompatible
			evidence = append(evidence, "module_root")
		default:
			kind = KindUnknown
		}
	}

	caps := Capabilities{}
	name := "Unknown"
	compatible := false
	switch kind {
	case KindMagisk:
		name, compatible = "Magisk", true
		caps = Capabilities{ModuleHooks: true, ServiceHook: true, PostFSDataHook: true, UninstallHook: true, ActionHook: true, UpdateStaging: true}
	case KindKernelSU:
		name, compatible = "KernelSU", true
		caps = Capabilities{ModuleHooks: true, ServiceHook: true, PostFSDataHook: true, UninstallHook: true, ActionHook: true, EmbeddedWebUI: true, UpdateStaging: true}
	case KindKernelSUNext:
		name, compatible = "KernelSU Next", true
		caps = Capabilities{ModuleHooks: true, ServiceHook: true, PostFSDataHook: true, UninstallHook: true, ActionHook: true, EmbeddedWebUI: true, UpdateStaging: true}
	case KindAPatch:
		name, compatible = "APatch", true
		caps = Capabilities{ModuleHooks: true, ServiceHook: true, PostFSDataHook: true, UninstallHook: true, ActionHook: true, EmbeddedWebUI: true, UpdateStaging: true}
	case KindCompatible:
		name, compatible = "Compatible root module manager", true
		// A common modules root proves only that a module layout exists. It does
		// not prove that Magisk-style service/post-fs-data/action/uninstall hooks
		// are implemented, and it never proves an embedded WebUI bridge. Keep
		// every unobserved capability false so callers can fail closed.
		caps = Capabilities{ModuleHooks: true}
	default:
		evidence = append(evidence, "no_supported_manager_evidence")
	}
	if dir(filepath.Join(adb, "modules_update")) {
		caps.UpdateStaging = caps.ModuleHooks
		evidence = append(evidence, "update_staging_root")
	}
	return Status{SchemaVersion: 1, Kind: kind, Name: name, Version: managerVersion(kind), Compatible: compatible, Capabilities: caps, Evidence: evidence}
}

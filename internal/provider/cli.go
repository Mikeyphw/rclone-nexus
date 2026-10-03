package provider

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"rclone-nexus/internal/paths"
	"rclone-nexus/internal/redact"
)

// UnsupportedMountFlags asks the actual selected provider binary for the
// rclone mount and global flag contracts, then returns generated long options
// the provider does not advertise. Command help intentionally omits many
// global flags in rclone, so mount --help alone is not authoritative for
// options such as --config and --rc-*.
func UnsupportedMountFlags(ctx context.Context, p paths.Paths, argv []string) ([]string, error) {
	binary, err := FindRclone(p)
	if err != nil {
		return nil, err
	}
	return UnsupportedMountFlagsForBinary(ctx, binary, argv)
}

// UnsupportedMountFlagsForBinary qualifies argv against the exact provider
// executable already selected by the caller. This prevents discovery drift
// between argv construction and compatibility qualification.
func UnsupportedMountFlagsForBinary(ctx context.Context, binary string, argv []string) ([]string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	// rclone subcommand help lists command-local flags, while global flags are
	// documented separately. Require mount help, then opportunistically merge
	// both global-help forms supported by current/older rclone releases.
	mountHelp, err := providerHelp(probeCtx, binary, "mount", "--help")
	if err != nil {
		if probeCtx.Err() != nil {
			return nil, fmt.Errorf("rclone mount help timed out: %w", probeCtx.Err())
		}
		return nil, fmt.Errorf("rclone mount help failed: %w", err)
	}
	helpParts := []string{mountHelp}
	if text, helpErr := providerHelp(probeCtx, binary, "help", "flags"); helpErr == nil {
		helpParts = append(helpParts, text)
	}
	if text, rootErr := providerHelp(probeCtx, binary, "--help"); rootErr == nil {
		helpParts = append(helpParts, text)
	}
	helpText := strings.Join(helpParts, "\n")

	flags := map[string]bool{}
	for _, arg := range argv {
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name := arg
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		flags[name] = true
	}
	missing := make([]string, 0)
	for flag := range flags {
		if !helpContainsFlag(helpText, flag) {
			missing = append(missing, flag)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func providerHelp(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	output := &limitedBuffer{remaining: 512 << 10}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return output.String(), nil
}

func helpContainsFlag(help, flag string) bool {
	for _, line := range strings.Split(help, "\n") {
		for _, token := range strings.Fields(line) {
			candidate := strings.TrimRight(token, ",")
			if candidate == flag || strings.HasPrefix(candidate, flag+"=") {
				return true
			}
		}
	}
	return false
}

func FormatMissingFlags(flags []string) string {
	if len(flags) == 0 {
		return ""
	}
	return redact.BoundedString("provider does not advertise mount option(s): "+strings.Join(flags, ", "), 2048)
}

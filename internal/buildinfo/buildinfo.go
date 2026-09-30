package buildinfo

const (
	Name        = "Rclone Nexus"
	Binary      = "racctl"
	ProtocolMin = 1
	ProtocolMax = 1
)

// Version is injected by the deterministic release build. Source builds retain
// the explicit development value so protocol output never depends on Git state.
var Version = "v0.1.0-dev"

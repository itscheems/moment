// Package buildinfo exposes the non-sensitive identity of the linked binary.
package buildinfo

// These values are overridden by release ldflags. Their defaults keep local
// and source builds deterministic and useful without relying on Git at runtime.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info is the stable, non-sensitive identity shared by CLI, HTTP diagnostics,
// and logs.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// Current returns a snapshot of the linked build metadata.
func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}

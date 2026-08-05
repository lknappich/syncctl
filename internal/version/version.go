// Package version holds build metadata injected at release time.
package version

import (
	"fmt"
	"runtime"
)

// Build-time variables, overridden by ldflags.
var (
	Version   = "0.0.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info aggregates version metadata.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	OS        string
	Arch      string
}

// Current returns the current build's version info.
func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// String returns a human-readable version banner.
func (i Info) String() string {
	return fmt.Sprintf("syncctl %s (commit=%s, built=%s, %s/%s)",
		i.Version, i.Commit, i.BuildDate, i.OS, i.Arch)
}

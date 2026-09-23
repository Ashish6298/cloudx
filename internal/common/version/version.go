package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the semantic version of CloudX.
	Version = "0.1.0-dev"
	// GitCommit is the git commit hash injected at build time.
	GitCommit = "unknown"
	// BuildDate is the RFC3339 timestamp injected at build time.
	BuildDate = "unknown"
)

// Info holds version and build metadata.
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

// Get returns the current version info.
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String returns formatted version string.
func (i Info) String() string {
	return fmt.Sprintf("CloudX %s (commit: %s, built: %s, %s, %s)",
		i.Version, i.GitCommit, i.BuildDate, i.GoVersion, i.Platform)
}

package internal

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		if (Version == "dev" || Version == "") && info.Main.Version != "" && info.Main.Version != "(devel)" {
			Version = info.Main.Version
		}
		if Commit == "none" || Commit == "" {
			var vcsCommit, vcsModified string
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					vcsCommit = setting.Value
				case "vcs.time":
					if Date == "unknown" || Date == "" {
						Date = setting.Value
					}
				case "vcs.modified":
					vcsModified = setting.Value
				}
			}
			if vcsCommit != "" {
				if vcsModified == "true" {
					Commit = vcsCommit + "-dirty"
				} else {
					Commit = vcsCommit
				}
			}
		}
	}
}

// VersionString returns a formatted version string including version, commit, date, and Go runtime details.
func VersionString() string {
	return fmt.Sprintf("uploadserver %s (commit: %s, built at: %s, runtime: %s %s/%s)",
		Version, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

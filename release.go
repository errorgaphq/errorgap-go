package errorgap

import (
	"os"
	"runtime/debug"
	"strings"
)

var releaseEnvKeys = []string{
	"ERRORGAP_RELEASE", "SOURCE_VERSION", "HEROKU_SLUG_COMMIT", "HEROKU_BUILD_COMMIT",
	"RENDER_GIT_COMMIT", "RAILWAY_GIT_COMMIT_SHA", "VERCEL_GIT_COMMIT_SHA", "KAMAL_VERSION",
	"GIT_COMMIT", "GIT_SHA",
}

// detectRelease works out which commit is running when Config.Release is
// empty: the commit variables hosting platforms export, then the revision
// the Go toolchain stamped into the binary (`go build` in a git checkout).
func detectRelease() string {
	for _, key := range releaseEnvKeys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return ""
}

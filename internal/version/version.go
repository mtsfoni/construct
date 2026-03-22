// Package version holds the build version and source directory, set via ldflags.
package version

// Version is set at build time via:
//
//	go build -ldflags "-X github.com/construct-run/construct/internal/version.Version=<ver>"
//
// Dev builds (no ldflags) use the sentinel value "dev".
var Version = "dev"

// SourceDir is the absolute path to the construct source tree at build time,
// set via:
//
//	go build -ldflags "-X github.com/construct-run/construct/internal/version.SourceDir=<path>"
//
// It is used by the daemon bootstrap to run `go build` from the correct
// directory when recompiling constructd. Empty when not stamped.
var SourceDir = ""

// IsDev returns true if this is a development build (no version stamped).
func IsDev() bool {
	return Version == "dev"
}

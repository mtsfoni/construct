// Package version holds the build version, set via ldflags.
package version

// Version is set at build time via:
//
//	go build -ldflags "-X github.com/construct-run/construct/internal/version.Version=<ver>"
//
// Dev builds (no ldflags) use the sentinel value "dev".
var Version = "dev"

// IsDev returns true if this is a development build (no version stamped).
func IsDev() bool {
	return Version == "dev"
}

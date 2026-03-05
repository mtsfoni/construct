package buildinfo

// Version is set at build time via:
//
//	-ldflags "-X github.com/mtsfoni/construct/internal/buildinfo.Version=<tag>"
//
// It is empty when the binary is built without ldflags (e.g. go build ./...
// during development). Callers must treat an empty Version as "dev build" and
// skip any version-dependent logic.
var Version string

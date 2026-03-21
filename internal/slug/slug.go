// Package slug derives a filesystem-safe identifier from a canonical folder path.
// Used by the quickstart and auth modules to create per-folder file paths.
package slug

import "strings"

const maxLen = 200

// FromPath derives a filesystem-safe slug from a canonical absolute path.
//
// Algorithm:
//  1. Replace all '/' with '_'.
//  2. Strip the leading '_' (from the leading '/').
//  3. Truncate to 200 characters.
func FromPath(canonicalPath string) string {
	s := strings.ReplaceAll(canonicalPath, "/", "_")
	s = strings.TrimPrefix(s, "_")
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

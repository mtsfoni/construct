// Package imagespec defines the abstraction for how a construct stack image
// is sourced. A Spec describes both how to obtain the image (build from an
// embedded Dockerfile, pull from a registry, build from a user-provided path)
// and produces a stable string label that uniquely identifies the desired image
// content. That label is stamped onto every built/pulled image so that the
// daemon can detect staleness without comparing image digests directly.
//
// Extension points:
//   - EmbeddedBuildSpec: builds from a Dockerfile embedded in the daemon binary.
//     The label is a SHA-256 hash of all files in the embedded build context,
//     so any change to a Dockerfile or entrypoint script triggers a rebuild.
//   - RegistrySpec (future): references a pre-built image by registry digest.
//     The label is the fully-qualified reference (e.g.
//     "ghcr.io/construct-run/stack-dotnet@sha256:abc…"). The daemon pulls
//     instead of building, and re-pulls when the pinned digest changes.
//   - CustomDockerfileSpec (future): builds from a user-provided Dockerfile on
//     the host. The label is a hash of the file contents.
package imagespec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
)

// Spec describes the desired source for a stack image.
type Spec interface {
	// Label returns a stable string that uniquely identifies the desired image
	// content. It is stamped as the io.construct.image-spec label on every
	// image built or pulled from this spec. The daemon compares this value
	// against the label on the existing image to decide whether to rebuild.
	Label() string
}

// EmbeddedBuildSpec builds an image from a build context embedded in the
// daemon binary. The label is a deterministic SHA-256 hash over the sorted
// file paths and contents of the embedded subtree, prefixed with "embedded:".
type EmbeddedBuildSpec struct {
	label string
}

// NewEmbeddedBuildSpec computes the content hash for the given embedded
// filesystem subtree and returns a ready-to-use EmbeddedBuildSpec.
// sub should be the fs.FS rooted at the stack's build context directory
// (i.e. the result of fs.Sub(embedfs.FS, "stacks/<name>")).
func NewEmbeddedBuildSpec(sub fs.FS) (*EmbeddedBuildSpec, error) {
	h, err := hashFS(sub)
	if err != nil {
		return nil, fmt.Errorf("hash build context: %w", err)
	}
	return &EmbeddedBuildSpec{label: "embedded:" + h}, nil
}

// Label implements Spec.
func (s *EmbeddedBuildSpec) Label() string { return s.label }

// hashFS computes a deterministic SHA-256 over all files in an fs.FS.
// Files are visited in sorted path order so the hash is stable regardless
// of filesystem traversal order. Both file paths and contents contribute to
// the hash, so renames and content changes both produce different hashes.
func hashFS(fsys fs.FS) (string, error) {
	type entry struct {
		path string
		data []byte
	}

	var entries []entry
	if err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		entries = append(entries, entry{path, data})
		return nil
	}); err != nil {
		return "", err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00", e.path)
		h.Write(e.data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

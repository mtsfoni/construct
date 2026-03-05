package stacks

import (
	"errors"
	"testing"

	"github.com/mtsfoni/construct/internal/buildinfo"
)

// stubImageLabel replaces the imageLabel variable for the duration of a test
// and restores it on cleanup.
func stubImageLabel(t *testing.T, fn func(imageName, label string) (string, error)) {
	t.Helper()
	orig := imageLabel
	imageLabel = fn
	t.Cleanup(func() { imageLabel = orig })
}

// stubVersion sets buildinfo.Version for the duration of a test and restores it.
func stubVersion(t *testing.T, v string) {
	t.Helper()
	orig := buildinfo.Version
	buildinfo.Version = v
	t.Cleanup(func() { buildinfo.Version = orig })
}

func TestImageVersionCurrent_DevBuild_AlwaysTrue(t *testing.T) {
	// When buildinfo.Version is empty (dev build), imageVersionCurrent must
	// return true regardless of what the image label says.
	stubVersion(t, "")
	// Stub returns a label that would cause a mismatch if the check ran.
	stubImageLabel(t, func(imageName, label string) (string, error) {
		return "v9.9.9", nil
	})

	if !imageVersionCurrent("any-image") {
		t.Error("expected true for dev build (empty version), got false")
	}
}

func TestImageVersionCurrent_MatchingVersion_ReturnsTrue(t *testing.T) {
	stubVersion(t, "v1.2.3")
	stubImageLabel(t, func(imageName, label string) (string, error) {
		return "v1.2.3", nil
	})

	if !imageVersionCurrent("construct-base") {
		t.Error("expected true when image label matches binary version, got false")
	}
}

func TestImageVersionCurrent_DifferentVersion_ReturnsFalse(t *testing.T) {
	stubVersion(t, "v1.2.3")
	stubImageLabel(t, func(imageName, label string) (string, error) {
		return "v1.0.0", nil
	})

	if imageVersionCurrent("construct-base") {
		t.Error("expected false when image label differs from binary version, got true")
	}
}

func TestImageVersionCurrent_NoLabel_ReturnsFalse(t *testing.T) {
	// An image built before this feature carries an empty label value.
	stubVersion(t, "v1.2.3")
	stubImageLabel(t, func(imageName, label string) (string, error) {
		return "", nil
	})

	if imageVersionCurrent("construct-base") {
		t.Error("expected false when image has no label (pre-feature image), got true")
	}
}

func TestImageVersionCurrent_InspectError_ReturnsFalse(t *testing.T) {
	// When docker image inspect fails (image not found), treat as stale.
	stubVersion(t, "v1.2.3")
	stubImageLabel(t, func(imageName, label string) (string, error) {
		return "", errors.New("docker: image not found")
	})

	if imageVersionCurrent("construct-base") {
		t.Error("expected false when inspect returns error, got true")
	}
}

func TestImageVersionCurrent_PassesCorrectArgs(t *testing.T) {
	// Verify that the correct image name and label key are forwarded.
	stubVersion(t, "v2.0.0")
	var gotImage, gotLabel string
	stubImageLabel(t, func(imageName, label string) (string, error) {
		gotImage = imageName
		gotLabel = label
		return "v2.0.0", nil
	})

	imageVersionCurrent("construct-go")

	if gotImage != "construct-go" {
		t.Errorf("imageLabel called with image %q, want %q", gotImage, "construct-go")
	}
	if gotLabel != "io.construct.version" {
		t.Errorf("imageLabel called with label %q, want %q", gotLabel, "io.construct.version")
	}
}

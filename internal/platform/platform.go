// Package platform checks host platform requirements before bootstrapping.
// construct requires Linux kernel 5.12 or later and Docker Engine 25.0 or later.
package platform

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Check verifies that the host meets the minimum platform requirements:
//   - Linux kernel ≥ 5.12
//   - Docker Engine ≥ 25.0
//
// dockerVersion should be the string returned by docker.ServerVersion()
// (e.g. "28.5.2"). Pass "" to skip the Docker version check (useful in tests).
func Check(dockerVersion string) error {
	if err := checkKernel(); err != nil {
		return err
	}
	if dockerVersion != "" {
		if err := checkDockerVersion(dockerVersion); err != nil {
			return err
		}
	}
	return nil
}

// checkKernel verifies the running Linux kernel is ≥ 5.12.
func checkKernel() error {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return fmt.Errorf("uname: %w", err)
	}
	release := utsRelease(uts.Release)
	major, minor, err := parseKernelVersion(release)
	if err != nil {
		return fmt.Errorf("parse kernel version %q: %w", release, err)
	}
	if major < 5 || (major == 5 && minor < 12) {
		return fmt.Errorf(
			"construct requires Linux kernel ≥ 5.12; running %d.%d",
			major, minor,
		)
	}
	return nil
}

// checkDockerVersion verifies the Docker Engine version is ≥ 25.0.
func checkDockerVersion(version string) error {
	major, minor, err := parseSemver(version)
	if err != nil {
		return fmt.Errorf("parse docker version %q: %w", version, err)
	}
	if major < 25 || (major == 25 && minor < 0) {
		return fmt.Errorf(
			"construct requires Docker Engine ≥ 25.0; running %s",
			version,
		)
	}
	return nil
}

// parseKernelVersion extracts major and minor from a kernel release string
// like "5.15.0-91-generic" or "6.1.0".
func parseKernelVersion(release string) (major, minor int, err error) {
	// Strip everything after the first non-numeric/dot character.
	clean := release
	for i, c := range release {
		if c != '.' && (c < '0' || c > '9') {
			clean = release[:i]
			break
		}
	}
	parts := strings.SplitN(clean, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected format")
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("major: %w", err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("minor: %w", err)
	}
	return major, minor, nil
}

// parseSemver extracts major.minor from a semver-ish string like "28.5.2" or "25.0".
func parseSemver(version string) (major, minor int, err error) {
	return parseKernelVersion(version)
}

// utsRelease converts the byte array from unix.Utsname.Release to a string.
func utsRelease(buf [65]byte) string {
	b := make([]byte, 0, 65)
	for _, c := range buf {
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

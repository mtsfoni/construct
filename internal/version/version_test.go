package version

import "testing"

func TestIsDev_Default(t *testing.T) {
	// The default value is "dev"
	if Version != "dev" {
		t.Skip("Version has been overridden at build time")
	}
	if !IsDev() {
		t.Error("IsDev() should be true when Version == \"dev\"")
	}
}

func TestIsDev_Stamped(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = "0.1.0"
	if IsDev() {
		t.Error("IsDev() should be false when version is stamped")
	}
}

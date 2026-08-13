package version

import "testing"

func TestBuildDefaultsAreNonEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must have a development default")
	}
	if GitSHA == "" {
		t.Fatal("GitSHA must have a development default")
	}
	if Edition == "" {
		t.Fatal("Edition must identify the build profile")
	}
}

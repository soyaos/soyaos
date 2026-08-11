package runtime

import "testing"

func TestImageRef_String(t *testing.T) {
	cases := []struct {
		ref  ImageRef
		want string
	}{
		{ImageRef{Name: "video-base", Version: "0.1.0"}, "video-base@0.1.0"},
		{ImageRef{Name: "video-base"}, "video-base"},
	}
	for _, tc := range cases {
		if got := tc.ref.String(); got != tc.want {
			t.Errorf("String()=%q, want %q", got, tc.want)
		}
	}
}

func TestVideoBase_MatchesManifest(t *testing.T) {
	// If anyone bumps deploy/comet-images/video-base/image.yaml they must
	// also update this constructor; the test pins the literal expected by
	// Stage 5 (DD-011) so the drift is caught at compile-test time.
	r := VideoBase()
	if r.Name != "video-base" {
		t.Errorf("Name=%q, want video-base", r.Name)
	}
	if r.Version != "0.1.0" {
		t.Errorf("Version=%q, want 0.1.0", r.Version)
	}
	if r.ColdStartTargetMS != 10000 {
		t.Errorf("ColdStartTargetMS=%d, want 10000", r.ColdStartTargetMS)
	}
}

func TestBuiltinImages_IncludesVideoBase(t *testing.T) {
	found := false
	for _, r := range BuiltinImages() {
		if r.Name == "video-base" {
			found = true
		}
	}
	if !found {
		t.Fatal("BuiltinImages() must include video-base")
	}
}

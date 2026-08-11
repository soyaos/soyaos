// SilentCut reverse-pressure: DD-011 wants the scheduler to address Comet
// base images by a stable `name@version` key (so SilentCut can say "give me
// video-base@0.1.0 to render an MP4") without each call site duplicating the
// version literal. This file is the canonical Go-side index of those images;
// the Dockerfile + manifest for each entry live under
// deploy/comet-images/<name>/. Keep both sides in sync — when a new image
// ships, add a row to deploy/comet-images/README.md and append a constructor
// here.

package runtime

// ImageRef is the address of one Comet base image. It maps 1:1 to a
// deploy/comet-images/<Name>/image.yaml entry and is the value carried in
// Task.Image / SandboxDecl.Image.
type ImageRef struct {
	// Name is the bare image name, e.g. "video-base". No registry / org
	// prefix — the registry resolution is the scheduler's job.
	Name string
	// Version is the SemVer tag, e.g. "0.1.0".
	Version string
	// ColdStartTargetMS is the cold-start SLA the scheduler optimizes for
	// when deciding microvm-snapshot vs. fresh provisioning. Mirrors the
	// `cold_start_target_ms` field in the manifest.
	ColdStartTargetMS int
}

// String returns the canonical "name@version" form used in Task.Image.
func (r ImageRef) String() string {
	if r.Version == "" {
		return r.Name
	}
	return r.Name + "@" + r.Version
}

// VideoBase is the SilentCut (DD-011) renderer image: Node 22 + Chromium +
// ffmpeg + Remotion CLI + Noto CJK + Inter.
func VideoBase() ImageRef {
	return ImageRef{Name: "video-base", Version: "0.1.0", ColdStartTargetMS: 10000}
}

// BuiltinImages returns the curated list of Comet base images this build of
// SoyaOS knows about. The list is authoritative for the scheduler at compile
// time; an out-of-tree image still works as long as Task.Image references
// it by string, but its cold-start target will be unknown to the planner.
func BuiltinImages() []ImageRef {
	return []ImageRef{
		VideoBase(),
	}
}

// remotion.go — SilentCut helper that wraps a Remotion CLI render
// invocation behind the same ExecuteRequest shape the rest of the
// runtime layer uses.
//
// DD-011 (SilentCut) reverse-pressure: in alpha we ship video
// rendering by shelling out to the user's local `npx remotion render`
// binary. The CometProvider contract already has a one-shot Execute
// path; the only thing missing is a deterministic recipe for "given
// a Remotion entry + composition id + props, here's the exact
// `[]string` argv my Execute should receive".
//
// Keeping this helper in `process` rather than a fresh package makes
// the alpha story honest: SilentCut today *is* the process tier
// shelling out to npx. When the container / microvm tiers land,
// SilentCut will lift this helper into a shared runtime utility.
//
// Capability gating: this file does NOT touch security. The gate
// (pkg/runtime.Gate) is the only authority that decides whether
// `npx` is on the allowlist; this helper just builds the argv.

package process

import (
	"errors"
	"path/filepath"

	"github.com/soyaos/soyaos/pkg/runtime"
)

// RemotionRenderSpec is the typed input to BuildRemotionExecuteRequest.
// Every field is required except PropsPath (which is the Remotion CLI
// surface — Remotion itself decides what's optional).
type RemotionRenderSpec struct {
	// EntryPoint is the path to the Remotion entry file
	// (typically src/index.ts under the project).
	EntryPoint string

	// CompositionID is the Remotion composition to render. Set in
	// the Composition's id="..." attribute inside the entry's Root.
	CompositionID string

	// OutputPath is where the rendered MP4 should land. Callers
	// typically pass an absolute path inside the sandbox's fs_write
	// allowlist (e.g. /workdir/out/clip.mp4).
	OutputPath string

	// PropsPath is an optional JSON file Remotion will hand to the
	// composition as input props. Empty ⇒ no --props flag.
	PropsPath string

	// Concurrency, FrameRange and Quality are forwarded verbatim
	// when non-zero — Remotion's defaults are usable so we don't
	// force callers to pin them.
	Concurrency int
	FrameRange  string
	Quality     int

	// NPXBinary lets the operator override the npx binary. Empty
	// defaults to "npx".
	NPXBinary string
}

// BuildRemotionExecuteRequest turns a RemotionRenderSpec into the
// runtime.ExecuteRequest the process provider should run. The Cmd
// is shaped as `npx remotion render <entry> <composition> <out>` plus
// flags — matching the official Remotion v4 CLI surface.
//
// The function is pure: it builds argv from spec and returns it. No
// disk I/O, no env reads. Callers (SilentCut Agent handler, integration
// tests) are responsible for validating that EntryPoint / OutputPath
// land inside their fs_read / fs_write allowlists before handing the
// request to Execute.
func BuildRemotionExecuteRequest(spec RemotionRenderSpec) (runtime.ExecuteRequest, error) {
	if spec.EntryPoint == "" {
		return runtime.ExecuteRequest{}, errors.New("remotion: EntryPoint is required")
	}
	if spec.CompositionID == "" {
		return runtime.ExecuteRequest{}, errors.New("remotion: CompositionID is required")
	}
	if spec.OutputPath == "" {
		return runtime.ExecuteRequest{}, errors.New("remotion: OutputPath is required")
	}
	// Reject relative paths — sandboxes don't have a stable CWD and
	// SilentCut's fs_write allowlist is absolute-path-only.
	if !filepath.IsAbs(spec.EntryPoint) {
		return runtime.ExecuteRequest{}, errors.New("remotion: EntryPoint must be absolute")
	}
	if !filepath.IsAbs(spec.OutputPath) {
		return runtime.ExecuteRequest{}, errors.New("remotion: OutputPath must be absolute")
	}

	npx := spec.NPXBinary
	if npx == "" {
		npx = "npx"
	}
	args := []string{
		npx,
		"remotion",
		"render",
		spec.EntryPoint,
		spec.CompositionID,
		spec.OutputPath,
	}
	if spec.PropsPath != "" {
		if !filepath.IsAbs(spec.PropsPath) {
			return runtime.ExecuteRequest{}, errors.New("remotion: PropsPath must be absolute when set")
		}
		args = append(args, "--props="+spec.PropsPath)
	}
	if spec.Concurrency > 0 {
		args = append(args, "--concurrency="+itoa(spec.Concurrency))
	}
	if spec.FrameRange != "" {
		args = append(args, "--frames="+spec.FrameRange)
	}
	if spec.Quality > 0 {
		// Remotion accepts --jpeg-quality for jpeg-stream-based renders
		// (the v4 default); we use the generic --quality alias that
		// remotion-cli normalizes internally.
		args = append(args, "--quality="+itoa(spec.Quality))
	}
	access := runtime.Access{
		FSRead:  []string{spec.EntryPoint},
		FSWrite: []string{spec.OutputPath},
	}
	if spec.PropsPath != "" {
		access.FSRead = append(access.FSRead, spec.PropsPath)
	}
	return runtime.ExecuteRequest{Cmd: args, Access: &access}, nil
}

// itoa is a small fast int-to-string helper — used a handful of times,
// not enough to bring strconv into this file for clarity.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

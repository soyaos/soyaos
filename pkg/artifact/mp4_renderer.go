// SilentCut reverse-pressure: DD-011 §R wants the MP4 artifact to land at
// the user's device while the renderer is still producing it. The actual
// frames come from Comet running Remotion inside the video-base image
// (APP-508); this file is the SoyaOS-side façade that turns that work into
// an Artifact + a chunked byte stream consumable from HTTP.
//
// alpha shape (APP-554):
//
//   - When the snapshot carries a *RemotionSpec, RenderStream spawns the
//     declared Remotion CLI process via os/exec, waits for it to finish, then
//     streams the actual output file through the chunks channel. Remotion's
//     stdout/stderr progress logs are kept separate from the MP4 bytes.
//
//   - When the snapshot has no spec (the legacy contract used by the
//     existing artifact + http_streaming tests), RenderStream falls back
//     to the original placeholder body: a valid-looking ISO/IEC 14496-12
//     `ftypisom` header + a small mdat-shaped payload split across ≥3
//     chunks. This keeps the streaming-contract tests passing on machines
//     that don't have Node + Remotion installed (i.e. CI).

package artifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// mp4FtypHeader is a minimal ISO/IEC 14496-12 `ftyp` box declaring the
// `isom` major brand. Exactly 24 bytes: 0x18 size, 'ftyp', major brand,
// minor version, then one compatible-brand entry. This makes the byte
// stream openable by file inspectors and by `file(1)` without us
// shipping a full muxer.
var mp4FtypHeader = []byte{
	0x00, 0x00, 0x00, 0x18, // box size = 24
	'f', 't', 'y', 'p',
	'i', 's', 'o', 'm', // major brand
	0x00, 0x00, 0x02, 0x00, // minor version
	'i', 's', 'o', 'm', // compatible brand
	'm', 'p', '4', '1', // compatible brand
}

// mp4PlaceholderPayload stands in for what would be `moov` + `mdat` in a
// real render. The streaming test asserts chunks split this evenly so
// chunk count > 1; keep it ≥ 24 bytes (the chunkSize default) for that
// invariant to hold.
var mp4PlaceholderPayload = []byte("soyaos-alpha-mp4-placeholder-payload-bytes-for-stage5-stub")

// RemotionSpec is the typed snapshot shape MP4Renderer reaches for when
// the caller wants a real Remotion render. It is intentionally close to
// runtime/providers/process.RemotionRenderSpec but lives here so the
// artifact package can be consumed without dragging in the runtime
// dependency tree.
//
// Argv is the full argv of the subprocess to spawn. Typically built via
// process.BuildRemotionExecuteRequest and copied here verbatim. The
// renderer does not validate argv — capability gating is the runtime
// layer's job; this façade just runs the command.
type RemotionSpec struct {
	// Argv is the full command to run (argv[0] is the binary).
	Argv []string
	// Stdin is optional bytes to feed on stdin (e.g. a JSON props blob).
	Stdin []byte
	// MaxStdoutBytes caps the streamed output; 0 ⇒ unlimited. Used as a
	// safety net for runaway renders; real production caps live in the
	// runtime layer's resource gate.
	MaxStdoutBytes int64
}

// commandRunner is the subprocess seam tests substitute. Production
// uses execCmdRunner, which wraps os/exec.CommandContext.
type commandRunner interface {
	Run(ctx context.Context, argv []string, stdin []byte, stdout chan<- []byte, maxBytes int64) error
}

// MP4Renderer renders the MP4 Artifact form. alpha emits a placeholder
// payload (see file comment) when the snapshot has no RemotionSpec; when
// it does, the streamed bytes come from a real Remotion subprocess.
type MP4Renderer struct {
	// Schema is the snapshot schema id stamped onto the produced Artifact.
	Schema string
	// ChunkSize is the byte budget per stream chunk. Zero ⇒ defaultChunkSize.
	ChunkSize int
	// Runner is the subprocess executor. Nil ⇒ execCmdRunner (os/exec).
	// Tests inject a fake to assert argv + stdin without spawning processes.
	Runner commandRunner
}

const defaultMP4ChunkSize = 24

// Kind reports KindMP4.
func (MP4Renderer) Kind() Kind { return KindMP4 }

// Render emits the alpha MP4-shaped payload to dst. The synchronous path
// exists so MP4Renderer satisfies the legacy Renderer contract; streaming
// callers should prefer RenderStream.
func (r MP4Renderer) Render(_ context.Context, snapshot any, dst io.Writer) (Artifact, error) {
	body := mp4Body()
	n, err := dst.Write(body)
	if err != nil {
		return Artifact{}, err
	}
	hash, _ := CanonicalHash(snapshot)
	return Artifact{
		Kind:         KindMP4,
		Schema:       r.Schema,
		SnapshotHash: hash,
		MIMEType:     "video/mp4",
		Size:         int64(n),
		CreatedAt:    time.Now(),
	}, nil
}

// RenderStream emits the MP4 body in order then closes chunks. When the
// snapshot is (or contains) a *RemotionSpec, the bytes come from a real
// Remotion subprocess; otherwise the renderer falls back to the alpha
// placeholder body for tests on machines without Remotion installed.
//
// The returned Artifact is identical to Render's except Streaming=true
// and Size=-1 (the body is of unknown length until the stream ends —
// even on the placeholder path we treat it as unknown so the same API
// works for live encoders).
func (r MP4Renderer) RenderStream(ctx context.Context, snapshot any, chunks chan<- []byte) (Artifact, error) {
	defer close(chunks)

	if spec, ok := extractRemotionSpec(snapshot); ok {
		runner := r.Runner
		if runner == nil {
			runner = execCmdRunner{}
		}
		if err := runner.Run(ctx, spec.Argv, spec.Stdin, chunks, spec.MaxStdoutBytes); err != nil {
			return Artifact{}, fmt.Errorf("artifact: remotion render: %w", err)
		}
		hash, _ := CanonicalHash(snapshot)
		return Artifact{
			Kind:         KindMP4,
			Schema:       r.Schema,
			SnapshotHash: hash,
			MIMEType:     "video/mp4",
			Size:         -1,
			Streaming:    true,
			CreatedAt:    time.Now(),
		}, nil
	}

	// Placeholder path — see file comment for why this exists.
	body := mp4Body()
	size := r.ChunkSize
	if size <= 0 {
		size = defaultMP4ChunkSize
	}
	// Ensure we produce at least 3 chunks regardless of ChunkSize, so the
	// streaming contract is observable even on tiny payloads.
	if len(body)/size < 3 {
		size = (len(body) + 2) / 3
		if size == 0 {
			size = 1
		}
	}
	for i := 0; i < len(body); i += size {
		end := i + size
		if end > len(body) {
			end = len(body)
		}
		select {
		case <-ctx.Done():
			return Artifact{}, ctx.Err()
		case chunks <- append([]byte(nil), body[i:end]...):
		}
	}
	hash, _ := CanonicalHash(snapshot)
	return Artifact{
		Kind:         KindMP4,
		Schema:       r.Schema,
		SnapshotHash: hash,
		MIMEType:     "video/mp4",
		Size:         -1,
		Streaming:    true,
		CreatedAt:    time.Now(),
	}, nil
}

// extractRemotionSpec returns the snapshot's *RemotionSpec if present.
// Three shapes are accepted so callers don't have to wrap:
//
//   - *RemotionSpec passed directly,
//   - RemotionSpec passed by value (we take its address),
//   - map[string]any{"remotion": <one of the above>} — the same JSON
//     shape an HTTP caller would POST.
func extractRemotionSpec(snapshot any) (*RemotionSpec, bool) {
	switch v := snapshot.(type) {
	case *RemotionSpec:
		if v != nil && len(v.Argv) > 0 {
			return v, true
		}
	case RemotionSpec:
		if len(v.Argv) > 0 {
			vv := v
			return &vv, true
		}
	case map[string]any:
		if inner, ok := v["remotion"]; ok {
			return extractRemotionSpec(inner)
		}
	}
	return nil, false
}

// execCmdRunner is the production commandRunner. Each call spawns one
// subprocess via os/exec.CommandContext. Canonical Remotion commands stream
// their output file after a successful exit; legacy commands stream stdout.
// Stderr is collected and folded into the returned error on non-zero exit.
type execCmdRunner struct{}

func (execCmdRunner) Run(ctx context.Context, argv []string, stdin []byte, chunks chan<- []byte, maxBytes int64) error {
	if len(argv) == 0 {
		return errors.New("artifact: empty Remotion argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	// `remotion render` writes the encoded MP4 to its output-path argument;
	// stdout is a progress log, not video bytes. Earlier alpha code streamed
	// that log and produced an invalid "MP4". Detect the canonical CLI shape,
	// wait for the render, then stream the real file. The legacy stdout path is
	// retained for other command runners and existing subprocess consumers.
	if outputPath, ok := remotionFileOutputPath(argv); ok {
		var stdoutBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("remotion exited: %w (stdout: %s; stderr: %s)", err, stdoutBuf.String(), stderrBuf.String())
		}
		if err := streamFileChunks(ctx, outputPath, chunks, maxBytes); err != nil {
			return fmt.Errorf("stream remotion output: %w", err)
		}
		return nil
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start remotion: %w", err)
	}

	// Pump stdout into chunks. We bound each read by the renderer's
	// chunk size so consumers see frequent progress; 64 KiB is a
	// typical OS pipe buffer step.
	buf := make([]byte, 64*1024)
	var streamed int64
	for {
		n, readErr := stdoutPipe.Read(buf)
		if n > 0 {
			out := append([]byte(nil), buf[:n]...)
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return ctx.Err()
			case chunks <- out:
				streamed += int64(n)
				if maxBytes > 0 && streamed > maxBytes {
					_ = cmd.Process.Kill()
					return fmt.Errorf("remotion: stdout exceeded MaxStdoutBytes=%d", maxBytes)
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			_ = cmd.Wait()
			return fmt.Errorf("remotion stdout: %w", readErr)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("remotion exited: %w (stderr: %s)", err, stderrBuf.String())
	}
	return nil
}

// remotionFileOutputPath recognizes the stable Remotion CLI sequence:
//
//	<runner> [runner flags] remotion render <entry> <composition> <output>
//
// Only a direct Remotion binary or an npx/bunx launcher is accepted. Callers
// still own capability gating; this helper only identifies where an already
// authorized Remotion command will write its MP4.
func remotionFileOutputPath(argv []string) (string, bool) {
	if len(argv) < 5 {
		return "", false
	}
	name := strings.TrimSuffix(filepath.Base(argv[0]), filepath.Ext(argv[0]))
	if name == "remotion" {
		if argv[1] == "render" && argv[4] != "" {
			return argv[4], true
		}
		return "", false
	}
	if name != "npx" && name != "bunx" {
		return "", false
	}
	for i := 1; i+4 < len(argv); i++ {
		if argv[i] == "remotion" && argv[i+1] == "render" && argv[i+4] != "" {
			return argv[i+4], true
		}
	}
	return "", false
}

func streamFileChunks(ctx context.Context, path string, chunks chan<- []byte, maxBytes int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 64*1024)
	var streamed int64
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			streamed += int64(n)
			if maxBytes > 0 && streamed > maxBytes {
				return fmt.Errorf("remotion output exceeded MaxStdoutBytes=%d", maxBytes)
			}
			out := append([]byte(nil), buf[:n]...)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case chunks <- out:
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

// mp4Body returns the alpha-stub MP4 body. Header + placeholder.
func mp4Body() []byte {
	var buf bytes.Buffer
	buf.Grow(len(mp4FtypHeader) + len(mp4PlaceholderPayload))
	buf.Write(mp4FtypHeader)
	buf.Write(mp4PlaceholderPayload)
	return buf.Bytes()
}

// Compile-time assertion that MP4Renderer satisfies StreamingRenderer.
var _ StreamingRenderer = MP4Renderer{}

// ErrChannelNil is returned by RenderStream callers that forget to allocate
// the channel. Kept package-private to avoid a noise import; revisit if a
// caller actually needs to errors.Is on it.
var errChannelNil = errors.New("artifact: RenderStream requires a non-nil chunks channel")

// _ = errChannelNil keeps the linter happy until we surface it.
var _ = errChannelNil

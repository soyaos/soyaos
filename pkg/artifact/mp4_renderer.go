// SilentCut reverse-pressure: DD-011 §R wants the MP4 artifact to land at
// the user's device while the renderer is still producing it. The actual
// frames come from Comet running Remotion inside the video-base image
// (APP-508); this file is the SoyaOS-side façade that turns that work into
// an Artifact + a chunked byte stream consumable from HTTP.
//
// alpha shape: Render emits a tiny but valid-looking MP4-shaped payload —
// the ISO/IEC 14496-12 `ftypisom` brand at offset 0 + a placeholder mdat
// payload — so downstream consumers see "non-empty, MP4-looking bytes"
// without us having to spawn Chromium / ffmpeg in unit tests. The Stage 5
// implementation will dispatch to a Comet handle and pipe its stdout
// through the same chunks channel.

package artifact

import (
	"bytes"
	"context"
	"errors"
	"io"
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

// MP4Renderer renders the MP4 Artifact form. alpha emits a placeholder
// payload (see file comment); Stage 5 wires Comet + Remotion.
type MP4Renderer struct {
	// Schema is the snapshot schema id stamped onto the produced Artifact.
	Schema string
	// ChunkSize is the byte budget per stream chunk. Zero ⇒ defaultChunkSize.
	ChunkSize int
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

// RenderStream emits the MP4 body as ≥3 chunks, in order, then closes
// chunks. The returned Artifact is identical to Render's except
// Streaming=true and Size=-1 (the body is theoretically of unknown
// length until the stream ends — we *know* it here, but the contract
// is to treat streams as unknown so the same API works for live encoders).
func (r MP4Renderer) RenderStream(ctx context.Context, snapshot any, chunks chan<- []byte) (Artifact, error) {
	defer close(chunks)
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

// SilentCut reverse-pressure: S3 (proper AWS plus the compatible cohort —
// MinIO, B2, R2, Wasabi) is where studios push masters destined for the
// CDN. Same five-method contract; wire implementation lands in Stage 5.

package nas

import (
	"context"
	"io"
)

// s3Handle is the alpha stub. Stage 5 will wrap AWS SDK v2.
//
// TODO(Stage5): AWS SDK v2 — github.com/aws/aws-sdk-go-v2/service/s3 with
// a custom endpoint resolver (so MinIO / B2 / R2 work) and SigV4 derived
// from Moon-issued temporary credentials.
type s3Handle struct {
	cfg Config
}

func openS3(_ context.Context, cfg Config) (NAS, error) {
	return &s3Handle{cfg: cfg}, nil
}

func (h *s3Handle) Write(context.Context, string, io.Reader) (int64, error) {
	return 0, ErrNotImplemented
}

func (h *s3Handle) Close() error {
	wipe(&h.cfg)
	return nil
}

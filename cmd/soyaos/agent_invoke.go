package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/artifact"
	"github.com/soyaos/soyaos/pkg/connectors/nas"
)

type mp4InvokeOptions struct {
	Schema             string
	Output             string
	RemotionProject    string
	SourceOutput       string
	EvidenceOutput     string
	RenderTimeout      time.Duration
	NASProtocol        string
	NASHost            string
	NASShare           string
	NASPath            string
	NASUsernameEnv     string
	NASPasswordEnv     string
	NASSessionTokenEnv string
	NASRegion          string
}

type mp4InvokeEvidence struct {
	Schema                    string `json:"schema"`
	Artifact                  string `json:"artifact"`
	Bytes                     int64  `json:"bytes"`
	SHA256                    string `json:"sha256"`
	DurationMS                int64  `json:"duration_ms"`
	ControlPlaneResponseBytes int    `json:"control_plane_response_bytes"`
	ArtifactDataPlaneBytes    int64  `json:"artifact_data_plane_bytes"`
	PlanetDataPlaneBytes      int64  `json:"planet_data_plane_bytes"`
	DataPlaneRoute            string `json:"data_plane_route"`
	NASProtocol               string `json:"nas_protocol,omitempty"`
	NASRemotePath             string `json:"nas_remote_path,omitempty"`
	NASBytes                  int64  `json:"nas_bytes,omitempty"`
	PortableSource            string `json:"portable_source,omitempty"`
}

type remotionEnvelope struct {
	Remotion struct {
		Argv  []string        `json:"argv"`
		Stdin string          `json:"stdin"`
		Props json.RawMessage `json:"props"`
	} `json:"remotion"`
}

func renderMP4Artifact(content string, opts mp4InvokeOptions) (mp4InvokeEvidence, error) {
	if opts.Output == "" {
		return mp4InvokeEvidence{}, errors.New("agent invoke: --output is required for mp4")
	}
	if opts.RemotionProject == "" {
		return mp4InvokeEvidence{}, errors.New("agent invoke: --remotion-project is required for mp4")
	}
	if opts.Schema == "" {
		opts.Schema = "short_video.v1"
	}
	if opts.RenderTimeout <= 0 {
		opts.RenderTimeout = 5 * time.Minute
	}

	body := stripJSONFence(content)
	var envelope remotionEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: response is not a valid Remotion snapshot: %w", err)
	}
	if len(envelope.Remotion.Props) == 0 || !json.Valid(envelope.Remotion.Props) {
		return mp4InvokeEvidence{}, errors.New("agent invoke: response.remotion.props must be valid JSON")
	}

	project, err := filepath.Abs(opts.RemotionProject)
	if err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: resolve Remotion project: %w", err)
	}
	entry := filepath.Join(project, "src", "index.ts")
	if _, err := os.Stat(entry); err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: Remotion entry %s: %w", entry, err)
	}
	remotionBinary, err := findRemotionBinary(project)
	if err != nil {
		return mp4InvokeEvidence{}, err
	}

	output, err := filepath.Abs(opts.Output)
	if err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: resolve output: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: create output directory: %w", err)
	}

	workDir, err := os.MkdirTemp("", "soyaos-silentcut-render-*")
	if err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: create render directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	propsPath := filepath.Join(workDir, "props.json")
	if err := os.WriteFile(propsPath, envelope.Remotion.Props, 0o600); err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: write Remotion props: %w", err)
	}
	renderedPath := filepath.Join(workDir, "rendered.mp4")
	argv := []string{
		remotionBinary,
		"render",
		entry,
		"SilentCutComposition",
		renderedPath,
		"--props=" + propsPath,
		"--concurrency=100%",
	}
	if chrome := os.Getenv("SOYAOS_CHROME"); chrome != "" {
		argv = append(argv, "--browser-executable="+chrome)
	}

	tmp, err := os.CreateTemp(filepath.Dir(output), "."+filepath.Base(output)+".tmp-*")
	if err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: create temporary MP4: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	ctx, cancel := context.WithTimeout(context.Background(), opts.RenderTimeout)
	defer cancel()
	chunks := make(chan []byte, 8)
	type renderResult struct {
		err error
	}
	result := make(chan renderResult, 1)
	started := time.Now()
	go func() {
		_, renderErr := (artifact.MP4Renderer{Schema: opts.Schema}).RenderStream(ctx, artifact.RemotionSpec{
			Argv:           argv,
			MaxStdoutBytes: 1 << 30,
		}, chunks)
		result <- renderResult{err: renderErr}
	}()

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), channelReader(chunks))
	renderErr := (<-result).err
	closeErr := tmp.Close()
	if copyErr != nil || renderErr != nil || closeErr != nil {
		return mp4InvokeEvidence{}, errors.Join(copyErr, renderErr, closeErr)
	}
	if err := validateMP4File(tmpPath); err != nil {
		return mp4InvokeEvidence{}, err
	}
	if err := os.Rename(tmpPath, output); err != nil {
		return mp4InvokeEvidence{}, fmt.Errorf("agent invoke: publish MP4: %w", err)
	}

	evidence := mp4InvokeEvidence{
		Schema:                    opts.Schema,
		Artifact:                  filepath.Base(output),
		Bytes:                     written,
		SHA256:                    hex.EncodeToString(hash.Sum(nil)),
		DurationMS:                time.Since(started).Milliseconds(),
		ControlPlaneResponseBytes: len(content),
		ArtifactDataPlaneBytes:    written,
		PlanetDataPlaneBytes:      0,
		DataPlaneRoute:            "direct",
	}

	if opts.SourceOutput != "" {
		if err := copyPortableRemotionProject(project, opts.SourceOutput, envelope.Remotion.Props); err != nil {
			return mp4InvokeEvidence{}, err
		}
		evidence.PortableSource = filepath.Base(filepath.Clean(opts.SourceOutput))
	}
	if opts.NASProtocol != "" {
		remotePath, n, err := uploadArtifactToNAS(ctx, output, opts)
		if err != nil {
			return mp4InvokeEvidence{}, err
		}
		evidence.NASProtocol = opts.NASProtocol
		evidence.NASRemotePath = remotePath
		evidence.NASBytes = n
	}
	if opts.EvidenceOutput != "" {
		if err := writeJSONAtomic(opts.EvidenceOutput, evidence); err != nil {
			return mp4InvokeEvidence{}, err
		}
	}
	return evidence, nil
}

// channelReader adapts the renderer's chunk channel to io.Reader without
// buffering an entire MP4 in memory.
func channelReader(ch <-chan []byte) io.Reader {
	return &chunkReader{chunks: ch}
}

type chunkReader struct {
	chunks <-chan []byte
	cur    []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		chunk, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		r.cur = chunk
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

func stripJSONFence(content string) string {
	body := strings.TrimSpace(content)
	if !strings.HasPrefix(body, "```") {
		return body
	}
	if newline := strings.IndexByte(body, '\n'); newline >= 0 {
		body = body[newline+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body), "```"))
}

func findRemotionBinary(project string) (string, error) {
	name := "remotion"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	binary := filepath.Join(project, "node_modules", ".bin", name)
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("agent invoke: Remotion is not installed at %s; run `bun install --frozen-lockfile` in %s", binary, project)
	}
	return binary, nil
}

func validateMP4File(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("agent invoke: open rendered MP4: %w", err)
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return fmt.Errorf("agent invoke: inspect rendered MP4: %w", err)
	}
	if n < 8 || !bytes.Contains(head[:n], []byte("ftyp")) {
		return errors.New("agent invoke: Remotion output is not a valid MP4 (missing ftyp box)")
	}
	return nil
}

func copyPortableRemotionProject(src, dst string, props []byte) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if rel, relErr := filepath.Rel(srcAbs, dstAbs); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("agent invoke: --source-output must be outside --remotion-project")
	}
	if _, err := os.Stat(dstAbs); err == nil {
		return fmt.Errorf("agent invoke: source output already exists: %s", dstAbs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	err = filepath.WalkDir(srcAbs, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcAbs, filePath)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dstAbs, 0o755)
		}
		if portableSourceExcluded(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dstAbs, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(filePath)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		inputCloseErr := in.Close()
		return errors.Join(copyErr, closeErr, inputCloseErr)
	})
	if err != nil {
		return fmt.Errorf("agent invoke: export Remotion source: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dstAbs, "props.json"), props, 0o644); err != nil {
		return fmt.Errorf("agent invoke: export Remotion props: %w", err)
	}
	return nil
}

func portableSourceExcluded(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		switch part {
		case ".git", "node_modules", "out", "dist", ".DS_Store":
			return true
		}
	}
	return false
}

func uploadArtifactToNAS(ctx context.Context, artifactPath string, opts mp4InvokeOptions) (string, int64, error) {
	if opts.NASHost == "" || opts.NASPath == "" {
		return "", 0, errors.New("agent invoke: --nas-host and --nas-path are required when --nas-protocol is set")
	}
	username, err := envCredential(opts.NASUsernameEnv, os.LookupEnv)
	if err != nil {
		return "", 0, err
	}
	password, err := envCredential(opts.NASPasswordEnv, os.LookupEnv)
	if err != nil {
		return "", 0, err
	}
	sessionToken, err := envCredential(opts.NASSessionTokenEnv, os.LookupEnv)
	if err != nil {
		return "", 0, err
	}
	handle, err := nas.Open(ctx, nas.Config{
		Protocol:         opts.NASProtocol,
		Host:             opts.NASHost,
		Share:            opts.NASShare,
		Bucket:           opts.NASShare,
		Endpoint:         opts.NASHost,
		Username:         username,
		Password:         password,
		SessionToken:     sessionToken,
		Region:           opts.NASRegion,
		NFSUseProcessIDs: true,
	})
	if err != nil {
		return "", 0, fmt.Errorf("agent invoke: open NAS target: %w", err)
	}
	defer handle.Close()

	remotePath := opts.NASPath
	if opts.NASProtocol == "webdav" && opts.NASShare != "" {
		remotePath = path.Join(strings.TrimPrefix(opts.NASShare, "/"), opts.NASPath)
	}
	f, err := os.Open(artifactPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	n, err := handle.Write(ctx, remotePath, f)
	if err != nil {
		return "", n, fmt.Errorf("agent invoke: write MP4 to NAS: %w", err)
	}
	return remotePath, n, nil
}

func writeJSONAtomic(filePath string, value any) error {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, abs); err != nil {
		return err
	}
	return nil
}

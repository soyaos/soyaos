//go:build integration

package nas

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hirochachacha/go-smb2"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	nfsclient "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

func TestNASWireIntegration(t *testing.T) {
	if os.Getenv("SOYA_NAS_E2E") != "1" {
		t.Skip("set SOYA_NAS_E2E=1 in the isolated NAS test environment")
	}
	payload := []byte(fmt.Sprintf("soyaos-nas-e2e-%d", time.Now().UnixNano()))
	name := fmt.Sprintf("soyaos-check/integration-%d.bin", time.Now().UnixNano())

	t.Run("smb", func(t *testing.T) {
		cfg := Config{
			Protocol: "smb", Host: mustEnv(t, "SOYA_NAS_SMB_HOST"), Share: mustEnv(t, "SOYA_NAS_SMB_SHARE"),
			Username: mustEnv(t, "SOYA_NAS_USER"), Password: mustEnv(t, "SOYA_NAS_PASSWORD"), Timeout: 20 * time.Second,
		}
		writeProbe(t, cfg, name, payload)
		got := readSMB(t, cfg, name)
		if !bytes.Equal(got, payload) {
			t.Fatalf("SMB read-back mismatch: got %d bytes", len(got))
		}
	})

	t.Run("nfs", func(t *testing.T) {
		cfg := Config{
			Protocol: "nfs", Host: mustEnv(t, "SOYA_NAS_NFS_HOST"), Share: mustEnv(t, "SOYA_NAS_NFS_EXPORT"),
			NFSUseProcessIDs: true, Timeout: 20 * time.Second,
		}
		writeProbe(t, cfg, name, payload)
		got := readNFS(t, cfg, name)
		if !bytes.Equal(got, payload) {
			t.Fatalf("NFS read-back mismatch: got %d bytes", len(got))
		}
	})

	t.Run("webdav", func(t *testing.T) {
		cfg := Config{
			Protocol: "webdav", Host: mustEnv(t, "SOYA_NAS_WEBDAV_URL"),
			Username: mustEnv(t, "SOYA_NAS_USER"), Password: mustEnv(t, "SOYA_NAS_PASSWORD"), Timeout: 20 * time.Second,
		}
		writeProbe(t, cfg, name, payload)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, joinURL(cfg.Host, name), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(cfg.Username, cfg.Password)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("WebDAV GET: %v", err)
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(got, payload) {
			t.Fatalf("WebDAV read-back: status=%d bytes=%d err=%v", resp.StatusCode, len(got), err)
		}
	})

	t.Run("s3", func(t *testing.T) {
		cfg := Config{
			Protocol: "s3", Host: mustEnv(t, "SOYA_NAS_S3_ENDPOINT"), Share: mustEnv(t, "SOYA_NAS_S3_BUCKET"),
			Bucket: mustEnv(t, "SOYA_NAS_S3_BUCKET"), Region: "us-east-1",
			Username: mustEnv(t, "SOYA_NAS_S3_ACCESS_KEY"), Password: mustEnv(t, "SOYA_NAS_S3_SECRET_KEY"), Timeout: 20 * time.Second,
		}
		client := newIntegrationS3Client(t, cfg)
		exists, err := client.BucketExists(context.Background(), cfg.Bucket)
		if err != nil {
			t.Fatalf("S3 BucketExists: %v", err)
		}
		if !exists {
			if err := client.MakeBucket(context.Background(), cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
				t.Fatalf("S3 MakeBucket: %v", err)
			}
		}
		writeProbe(t, cfg, name, payload)
		obj, err := client.GetObject(context.Background(), cfg.Bucket, name, minio.GetObjectOptions{})
		if err != nil {
			t.Fatalf("S3 GetObject: %v", err)
		}
		got, err := io.ReadAll(obj)
		closeErr := obj.Close()
		if err != nil || closeErr != nil || !bytes.Equal(got, payload) {
			t.Fatalf("S3 read-back: bytes=%d readErr=%v closeErr=%v", len(got), err, closeErr)
		}
		if err := client.RemoveObject(context.Background(), cfg.Bucket, name, minio.RemoveObjectOptions{}); err != nil {
			t.Fatalf("S3 cleanup object: %v", err)
		}
	})
}

func writeProbe(t *testing.T, cfg Config, name string, payload []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open %s: %v", cfg.Protocol, err)
	}
	n, writeErr := h.Write(ctx, name, bytes.NewReader(payload))
	closeErr := h.Close()
	if writeErr != nil || closeErr != nil || n != int64(len(payload)) {
		t.Fatalf("Write %s: bytes=%d writeErr=%v closeErr=%v", cfg.Protocol, n, writeErr, closeErr)
	}
}

func readSMB(t *testing.T, cfg Config, name string) []byte {
	t.Helper()
	addr, err := smbAddress(cfg.Host)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatalf("SMB verify dial: %v", err)
	}
	session, err := (&smb2.Dialer{Initiator: &smb2.NTLMInitiator{User: cfg.Username, Password: cfg.Password, Domain: cfg.Domain}}).Dial(conn)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("SMB verify auth: %v", err)
	}
	share, err := session.Mount(cfg.Share)
	if err != nil {
		_ = session.Logoff()
		_ = conn.Close()
		t.Fatalf("SMB verify mount: %v", err)
	}
	f, err := share.Open(name)
	if err != nil {
		t.Fatalf("SMB verify open: %v", err)
	}
	got, readErr := io.ReadAll(f)
	_ = f.Close()
	_ = share.Umount()
	_ = session.Logoff()
	_ = conn.Close()
	if readErr != nil {
		t.Fatalf("SMB verify read: %v", readErr)
	}
	return got
}

func readNFS(t *testing.T, cfg Config, name string) []byte {
	t.Helper()
	mount, err := nfsclient.DialMount(cfg.Host, 20*time.Second)
	if err != nil {
		t.Fatalf("NFS verify dial: %v", err)
	}
	machine, _ := os.Hostname()
	target, err := mount.Mount(cfg.Share, rpc.NewAuthUnix(machine, uint32(os.Getuid()), uint32(os.Getgid())).Auth())
	if err != nil {
		mount.Client.Close()
		t.Fatalf("NFS verify mount: %v", err)
	}
	f, err := target.Open(name)
	if err != nil {
		t.Fatalf("NFS verify open: %v", err)
	}
	got, readErr := io.ReadAll(f)
	_ = f.Close()
	target.Client.Close()
	_ = mount.Unmount()
	mount.Client.Close()
	if readErr != nil {
		t.Fatalf("NFS verify read: %v", readErr)
	}
	return got
}

func newIntegrationS3Client(t *testing.T, cfg Config) *minio.Client {
	t.Helper()
	endpoint, secure, err := s3Endpoint(cfg.Host)
	if err != nil {
		t.Fatal(err)
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.Username, cfg.Password, ""), Secure: secure, Region: cfg.Region, BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func mustEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("required integration environment variable %s is missing", name)
	}
	return value
}

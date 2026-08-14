package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/connectors/nas"
)

var errNASCheckFailed = errors.New("NAS compatibility probe failed; see JSON result")

type nasCheckResult struct {
	Protocol   string `json:"protocol"`
	Success    bool   `json:"success"`
	Bytes      int64  `json:"bytes"`
	RemotePath string `json:"remote_path"`
	DurationMS int64  `json:"duration_ms"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

func cmdChannel(args []string) error {
	if len(args) >= 2 && args[0] == "bind" && args[1] == "nas" {
		return runNASCheck(args[2:], os.Stdout, os.LookupEnv)
	}
	return errors.New("usage: soyaos channel bind nas [flags]")
}

func cmdNAS(args []string) error {
	if len(args) >= 1 && args[0] == "check" {
		return runNASCheck(args[1:], os.Stdout, os.LookupEnv)
	}
	return errors.New("usage: soyaos nas check [flags]")
}

func runNASCheck(args []string, out io.Writer, getenv func(string) (string, bool)) error {
	fs := flag.NewFlagSet("channel bind nas", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	protocol := fs.String("protocol", "", "smb, nfs, webdav, or s3")
	host := fs.String("host", "", "SMB/NFS host, WebDAV base URL, or S3 endpoint URL")
	share := fs.String("share", "", "SMB share, NFS export, or S3 bucket")
	remotePath := fs.String("path", "", "relative probe path; generated when omitted")
	usernameEnv := fs.String("username-env", "", "environment variable containing username/access key")
	passwordEnv := fs.String("password-env", "", "environment variable containing password/secret key")
	sessionTokenEnv := fs.String("session-token-env", "", "optional S3 session-token environment variable")
	domain := fs.String("domain", "", "optional SMB domain")
	region := fs.String("region", "us-east-1", "S3 region")
	timeout := fs.Duration("timeout", 20*time.Second, "overall probe timeout")
	payloadBytes := fs.Int("payload-bytes", 128, "random probe size (1..1048576)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("NAS check does not accept positional arguments")
	}
	if *payloadBytes < 1 || *payloadBytes > 1<<20 || *timeout <= 0 {
		return errors.New("payload-bytes must be 1..1048576 and timeout must be positive")
	}

	username, err := envCredential(*usernameEnv, getenv)
	if err != nil {
		return err
	}
	password, err := envCredential(*passwordEnv, getenv)
	if err != nil {
		return err
	}
	sessionToken, err := envCredential(*sessionTokenEnv, getenv)
	if err != nil {
		return err
	}

	probeID := make([]byte, 12)
	if _, err := rand.Read(probeID); err != nil {
		return fmt.Errorf("generate probe id: %w", err)
	}
	if *remotePath == "" {
		*remotePath = "soyaos-check/" + hex.EncodeToString(probeID) + ".bin"
	}
	payload := make([]byte, *payloadBytes)
	if _, err := rand.Read(payload); err != nil {
		return fmt.Errorf("generate probe payload: %w", err)
	}

	result := nasCheckResult{Protocol: *protocol, RemotePath: *remotePath}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	handle, checkErr := nas.Open(ctx, nas.Config{
		Protocol:         *protocol,
		Host:             *host,
		Share:            *share,
		Username:         username,
		Password:         password,
		Domain:           *domain,
		Bucket:           *share,
		Region:           *region,
		Endpoint:         *host,
		SessionToken:     sessionToken,
		NFSUseProcessIDs: true,
		Timeout:          *timeout,
	})
	if checkErr == nil {
		result.Bytes, checkErr = handle.Write(ctx, *remotePath, bytes.NewReader(payload))
		checkErr = errors.Join(checkErr, handle.Close())
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.Success = checkErr == nil && result.Bytes == int64(len(payload))
	if !result.Success {
		if checkErr == nil {
			checkErr = io.ErrShortWrite
		}
		result.ErrorCode, result.Error = classifyNASError(checkErr)
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		return fmt.Errorf("encode NAS check result: %w", err)
	}
	if !result.Success {
		return errNASCheckFailed
	}
	return nil
}

func envCredential(name string, getenv func(string) (string, bool)) (string, error) {
	if name == "" {
		return "", nil
	}
	value, ok := getenv(name)
	if !ok || value == "" {
		return "", fmt.Errorf("credential environment variable %q is not set", name)
	}
	return value, nil
}

func classifyNASError(err error) (string, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout", "probe timed out or was canceled"
	case errors.Is(err, nas.ErrInvalidConfig):
		return "configuration", "binding configuration is invalid"
	case errors.Is(err, os.ErrPermission):
		return "authorization", "authentication or write permission was rejected"
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "auth") || strings.Contains(lower, "access denied") || strings.Contains(lower, "permission") || strings.Contains(lower, "status 401") || strings.Contains(lower, "status 403") {
		return "authorization", "authentication or write permission was rejected"
	}
	if strings.Contains(lower, "dial") || strings.Contains(lower, "connect") || strings.Contains(lower, "no such host") || strings.Contains(lower, "connection") {
		return "network", "NAS endpoint could not be reached"
	}
	return "io", "NAS write failed"
}

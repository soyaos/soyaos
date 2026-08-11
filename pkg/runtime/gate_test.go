package runtime

import (
	"errors"
	"testing"
)

func TestGateZeroDeniesAll(t *testing.T) {
	g := NewGate(Caps{})
	if err := g.CheckEgress("api.openai.com", 443, "https"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("zero Caps must deny egress, got %v", err)
	}
	if err := g.CheckFSRead("/workdir/in.txt"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("zero Caps must deny fs_read, got %v", err)
	}
	if err := g.CheckFSWrite("/workdir/out.pdf"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("zero Caps must deny fs_write, got %v", err)
	}
	if err := g.CheckExec("/usr/bin/curl"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("zero Caps must deny exec, got %v", err)
	}
}

func TestGateEgressAllowlist(t *testing.T) {
	g := NewGate(Caps{NetworkOut: []NetRule{
		{Host: "api.openai.com", Port: 443, Proto: "https"},
		{Host: "*", Port: 53, Proto: "udp"},
	}})

	cases := []struct {
		host  string
		port  int
		proto string
		want  bool // true = allowed
	}{
		{"api.openai.com", 443, "https", true},
		{"api.openai.com", 443, "HTTPS", true}, // proto case-insensitive
		{"api.openai.com", 80, "https", false}, // wrong port
		{"api.openai.com", 443, "http", false}, // wrong proto
		{"evil.example.com", 443, "https", false},
		{"resolver-1.cloud", 53, "udp", true}, // wildcard host
	}
	for _, c := range cases {
		err := g.CheckEgress(c.host, c.port, c.proto)
		got := err == nil
		if got != c.want {
			t.Errorf("CheckEgress(%q,%d,%q): want allowed=%v, got %v", c.host, c.port, c.proto, c.want, err)
		}
		if !c.want {
			var d *DeniedError
			if !errors.As(err, &d) {
				t.Errorf("denied egress must return *DeniedError, got %T", err)
			} else if d.Capability != "network_out" {
				t.Errorf("DeniedError.Capability = %q, want network_out", d.Capability)
			}
		}
	}
}

func TestGateFSAllowlist(t *testing.T) {
	g := NewGate(Caps{FSRead: []string{"/workdir"}, FSWrite: []string{"/workdir/out"}})

	if err := g.CheckFSRead("/workdir/in.txt"); err != nil {
		t.Errorf("expected /workdir/in.txt readable, got %v", err)
	}
	if err := g.CheckFSRead("/workdir"); err != nil {
		t.Errorf("expected /workdir itself readable, got %v", err)
	}
	if err := g.CheckFSRead("/workdir-evil/in.txt"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("boundary-evading prefix must be denied, got %v", err)
	}
	if err := g.CheckFSRead("workdir/in.txt"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("relative path must be denied, got %v", err)
	}

	if err := g.CheckFSWrite("/workdir/out/foo.pdf"); err != nil {
		t.Errorf("expected /workdir/out/foo.pdf writable, got %v", err)
	}
	if err := g.CheckFSWrite("/workdir/in.txt"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("write outside allowed root must be denied, got %v", err)
	}
}

func TestGateExecAllowlist(t *testing.T) {
	g := NewGate(Caps{Exec: []string{"chromium", "ffmpeg"}})

	if err := g.CheckExec("/usr/bin/chromium"); err != nil {
		t.Errorf("basename chromium must be allowed, got %v", err)
	}
	if err := g.CheckExec("ffmpeg"); err != nil {
		t.Errorf("bare ffmpeg must be allowed, got %v", err)
	}
	if err := g.CheckExec("/usr/bin/sh"); !errors.Is(err, ErrDeniedByCapability) {
		t.Errorf("sh not in allowlist, must be denied, got %v", err)
	}
}

func TestGateSnapshotIsCopied(t *testing.T) {
	rules := []NetRule{{Host: "api.openai.com", Port: 443, Proto: "https"}}
	g := NewGate(Caps{NetworkOut: rules})
	rules[0].Host = "evil.example.com" // mutate the source after construction
	if err := g.CheckEgress("api.openai.com", 443, "https"); err != nil {
		t.Errorf("gate must retain its snapshot, got %v", err)
	}
}

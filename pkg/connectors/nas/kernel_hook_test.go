package nas

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeEnv builds an EnvResolver backed by a static map.
func fakeEnv(m map[string]string) EnvResolver {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

func TestKernelHookAdapter_LiteralHost(t *testing.T) {
	env := fakeEnv(nil)
	out, err := KernelHookAdapter(context.Background(), BindingDecl{
		ID:       "primary",
		Protocol: "webdav",
		HostRef:  "https://files.example.com",
		Share:    "/videos",
	}, env)
	if err != nil {
		t.Fatalf("KernelHookAdapter: %v", err)
	}
	if out.Protocol != "webdav" || out.BasePath != "/videos" {
		t.Errorf("HookResult = %+v", out)
	}
	if out.Handle == nil {
		t.Fatal("Handle is nil")
	}
	_ = out.Handle.Close()
}

func TestKernelHookAdapter_EnvHostResolved(t *testing.T) {
	env := fakeEnv(map[string]string{
		"SOYA_NAS_HOST": "https://nas.lan",
		"SOYA_NAS_USER": "soya",
		"SOYA_NAS_PASS": "swordfish",
	})
	out, err := KernelHookAdapter(context.Background(), BindingDecl{
		Protocol: "webdav",
		HostRef:  "${SOYA_NAS_HOST}",
		Share:    "/videos",
		Secrets: map[string]string{
			"username_ref": "${SOYA_NAS_USER}",
			"password_ref": "${SOYA_NAS_PASS}",
		},
	}, env)
	if err != nil {
		t.Fatalf("KernelHookAdapter: %v", err)
	}
	if out.Handle == nil {
		t.Fatal("Handle is nil")
	}
	_ = out.Handle.Close()
}

func TestKernelHookAdapter_UnresolvedHostRef(t *testing.T) {
	env := fakeEnv(nil)
	_, err := KernelHookAdapter(context.Background(), BindingDecl{
		Protocol: "webdav",
		HostRef:  "${MISSING}",
		Share:    "/v",
	}, env)
	if err == nil {
		t.Fatal("expected error for unresolved ref, got nil")
	}
	if !errors.Is(err, ErrUnresolvedHostRef) {
		t.Errorf("error should wrap ErrUnresolvedHostRef, got %v", err)
	}
}

func TestKernelHookAdapter_UnresolvedSecretRef(t *testing.T) {
	_, err := KernelHookAdapter(context.Background(), BindingDecl{
		Protocol: "webdav",
		HostRef:  "https://nas.invalid",
		Share:    "/v",
		Secrets:  map[string]string{"password_ref": "${MISSING_PASSWORD}"},
	}, fakeEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "password_ref") {
		t.Fatalf("err=%v, want unresolved password_ref", err)
	}
}

func TestKernelHookAdapter_UnknownProtocol(t *testing.T) {
	env := fakeEnv(nil)
	_, err := KernelHookAdapter(context.Background(), BindingDecl{
		Protocol: "carrier-pigeon",
		HostRef:  "https://x",
		Share:    "/v",
	}, env)
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
	if !strings.Contains(err.Error(), "carrier-pigeon") && !errors.Is(err, ErrUnknownProtocol) {
		t.Errorf("error message = %v", err)
	}
}

func TestKernelHookAdapter_NilResolver(t *testing.T) {
	_, err := KernelHookAdapter(context.Background(), BindingDecl{Protocol: "webdav", HostRef: "x", Share: "/y"}, nil)
	if err == nil {
		t.Fatal("nil resolver must error")
	}
}

// kernel_hook.go — thin glue between pkg/connectors/nas and the
// kernel's NASHook surface (DD-011 SilentCut — APP-554).
//
// pkg/kernel must not import pkg/connectors/nas (that would force every
// kernel consumer to drag in the NAS drivers — go-smb2, AWS SDK, etc.
// when those land). Instead, the kernel declares the minimum surface
// it needs as kernel.NASWriter / kernel.NASBindingSpec / NASTarget.
// Since pkg/connectors/nas.NAS already exposes the same two methods
// the kernel asks for, the adapter is mostly env-var resolution.
//
// Wiring shape (from the host's main):
//
//   k.SetNASHook(func(decl kernel.NASBindingSpec) (kernel.NASTarget, error) {
//       r, err := nas.KernelHookAdapter(ctx, nas.BindingDecl{ ... }, os.LookupEnv)
//       if err != nil {
//           return kernel.NASTarget{}, err
//       }
//       return kernel.NASTarget{
//           ID: r.ID, Protocol: r.Protocol, BasePath: r.BasePath, Handle: r.Handle,
//       }, nil
//   })

package nas

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// EnvResolver dereferences ${ENV_NAME} refs. Implementations typically
// wrap os.LookupEnv, but tests can substitute a closure-backed
// dictionary so coverage doesn't depend on the test runner's
// environment.
type EnvResolver func(ref string) (string, bool)

// BindingDecl is the input shape KernelHookAdapter consumes. It
// mirrors kernel.NASBindingSpec field-for-field; the duplication is
// the price of avoiding the circular import.
type BindingDecl struct {
	ID       string
	Protocol string
	HostRef  string
	Share    string
	Access   string
	Secrets  map[string]string
}

// HookResult is the typed output of one KernelHookAdapter call. The
// caller (kernel.NASHook trampoline in cmd/soyaos*) maps these fields
// directly onto kernel.NASTarget without further translation; the
// Handle satisfies kernel.NASWriter as-is.
type HookResult struct {
	ID       string
	Protocol string
	BasePath string
	Handle   NAS
}

// ErrUnresolvedHostRef is returned when the manifest's host_ref is a
// ${ENV_NAME} string and the resolver can't find the env var.
var ErrUnresolvedHostRef = errors.New("nas: storage_nas.host_ref env var not set")

// KernelHookAdapter resolves one manifest.storage_nas[] entry into a
// live NAS handle. Returns HookResult on success.
//
//   - protocol must be one of "smb", "nfs", "webdav", "s3"; all four open
//     real wire clients.
//   - host_ref may be a literal URL or "${ENV_NAME}". The EnvResolver
//     fills in the latter.
//   - username / password are pulled from Secrets["username_ref"] /
//     Secrets["password_ref"] (each a ${ENV_NAME} ref). SMB may additionally
//     use domain_ref; S3 may use region_ref and session_token_ref.
//
// Closing the returned Handle is the caller's responsibility — the
// kernel owns the handle once stored in nasTargets and closes it on
// shutdown.
func KernelHookAdapter(ctx context.Context, decl BindingDecl, env EnvResolver) (HookResult, error) {
	if env == nil {
		return HookResult{}, errors.New("nas: KernelHookAdapter requires an EnvResolver")
	}
	host, err := resolveRefOrLiteral(decl.HostRef, env)
	if err != nil {
		return HookResult{}, fmt.Errorf("%w: %s", ErrUnresolvedHostRef, decl.HostRef)
	}
	username, err := resolveSecret(decl.Secrets["username_ref"], env)
	if err != nil {
		return HookResult{}, fmt.Errorf("nas: resolve username_ref: %w", err)
	}
	password, err := resolveSecret(decl.Secrets["password_ref"], env)
	if err != nil {
		return HookResult{}, fmt.Errorf("nas: resolve password_ref: %w", err)
	}
	domain, err := resolveSecret(decl.Secrets["domain_ref"], env)
	if err != nil {
		return HookResult{}, fmt.Errorf("nas: resolve domain_ref: %w", err)
	}
	region, err := resolveSecret(decl.Secrets["region_ref"], env)
	if err != nil {
		return HookResult{}, fmt.Errorf("nas: resolve region_ref: %w", err)
	}
	sessionToken, err := resolveSecret(decl.Secrets["session_token_ref"], env)
	if err != nil {
		return HookResult{}, fmt.Errorf("nas: resolve session_token_ref: %w", err)
	}

	cfg := Config{
		Protocol:         decl.Protocol,
		Host:             host,
		Share:            decl.Share,
		Username:         username,
		Password:         password,
		Domain:           domain,
		Bucket:           decl.Share,
		Region:           region,
		Endpoint:         host,
		SessionToken:     sessionToken,
		NFSUseProcessIDs: true,
	}
	handle, err := Open(ctx, cfg)
	if err != nil {
		return HookResult{}, fmt.Errorf("nas: open %s: %w", decl.Protocol, err)
	}
	return HookResult{
		ID:       decl.ID,
		Protocol: decl.Protocol,
		BasePath: decl.Share,
		Handle:   handle,
	}, nil
}

// resolveRefOrLiteral returns ref directly when it's not ${...}; when
// it is, calls env to look it up. Empty refs are an error (the
// validator already requires host_ref to be non-empty, but defence
// in depth).
func resolveRefOrLiteral(ref string, env EnvResolver) (string, error) {
	if ref == "" {
		return "", errors.New("empty ref")
	}
	if !strings.HasPrefix(ref, "${") || !strings.HasSuffix(ref, "}") {
		return ref, nil
	}
	name := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
	if v, ok := env(name); ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("env var %q not set", name)
}

// resolveSecret is resolveRefOrLiteral with an empty-input shortcut:
// when the manifest doesn't carry a given secret key (e.g. no
// password_ref), we return "" without error.
func resolveSecret(ref string, env EnvResolver) (string, error) {
	if ref == "" {
		return "", nil
	}
	return resolveRefOrLiteral(ref, env)
}

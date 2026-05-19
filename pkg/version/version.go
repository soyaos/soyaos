// Package version exposes build identification for the soyaos binary.
//
// Values are injected at build time via -ldflags in the top-level Makefile.
// Defaults below are used when building without the Makefile.
package version

// Version is the SemVer string (e.g. "0.1.0-alpha.0"). Per DD-003, SoyaOS
// starts at 0.1.0 and the API is unstable until 1.0.0.
var Version = "0.1.0-alpha.0"

// GitSHA is the short commit SHA, injected at build time.
var GitSHA = "unknown"

// Edition reports the SoyaOS edition this binary was built for. The Solo
// edition is the only edition supported in this pre-release; other editions
// will be selectable via build tags in later milestones.
const Edition = "solo"

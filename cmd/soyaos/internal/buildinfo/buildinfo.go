// Package buildinfo formats build identification for human display.
//
// Lives in internal/ so the layout matches the README ("not for external
// import"); pkg/version is the externally visible surface.
package buildinfo

import (
	"fmt"
	"io"
	"runtime"

	"github.com/soyaos/soyaos/pkg/version"
)

// Print writes a multi-line build-identification block to w.
func Print(w io.Writer) {
	fmt.Fprintf(w, "soyaos %s\n", version.Version)
	fmt.Fprintf(w, "  edition: %s\n", version.Edition)
	fmt.Fprintf(w, "  commit:  %s\n", version.GitSHA)
	fmt.Fprintf(w, "  go:      %s\n", runtime.Version())
	fmt.Fprintf(w, "  os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

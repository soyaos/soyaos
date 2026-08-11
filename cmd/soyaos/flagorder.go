package main

import (
	"flag"
	"strings"
)

// reorderForFlagSet sorts args so every recognized flag appears before any
// positional argument, matching the order users actually type subcommands.
//
// Go's stdlib `flag` package stops parsing at the first non-flag token, which
// makes intuitive invocations like
//
//	soyaos agent run llm "hello" --listen URL --key sk-...
//
// silently fail: --listen / --key get treated as positional content, fall back
// to defaults, and the request lands on the wrong port. This helper, called
// after flag definition but before fs.Parse, lifts the trailing flags back
// to the front so both orders work identically.
//
// Rules:
//   - "--" stops reordering: every token afterwards stays positional verbatim.
//   - "--flag=value" / "-flag=value" are single tokens — kept as-is.
//   - "--flag value" / "-flag value": when fs knows the flag and it is not a
//     bool flag, the helper pulls the next token along as the value. Unknown
//     flags or bool flags keep moving as a single token; fs.Parse will then
//     produce the usual "flag provided but not defined" error.
//   - "-" by itself is treated as positional (conventional stdin marker).
//   - Relative order is preserved within both groups.
func reorderForFlagSet(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	stopped := false

	for i := 0; i < len(args); i++ {
		tok := args[i]

		if stopped {
			positional = append(positional, tok)
			continue
		}
		if tok == "--" {
			stopped = true
			continue
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			positional = append(positional, tok)
			continue
		}

		flags = append(flags, tok)

		// `--flag=value` is self-contained.
		if strings.Contains(tok, "=") {
			continue
		}

		// Look up the flag to decide whether a separate value token follows.
		name := strings.TrimLeft(tok, "-")
		f := fs.Lookup(name)
		if f == nil {
			// Unknown flag — let fs.Parse report it. Don't consume the next
			// token; it might be a real positional or another flag.
			continue
		}
		if isBool(f) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positional...)
}

func isBool(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

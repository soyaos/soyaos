package main

import (
	"flag"
	"reflect"
	"testing"
)

func newRunLikeFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("agent run", flag.ContinueOnError)
	fs.String("listen", "http://127.0.0.1:7474", "")
	fs.String("key", "sk-soya-dev-local", "")
	fs.Bool("verbose", false, "")
	return fs
}

func TestReorderFlagsBeforePositional(t *testing.T) {
	fs := newRunLikeFlagSet()
	in := []string{"llm", "hello world", "--listen", "http://x:1", "--key", "sk-abc"}
	want := []string{"--listen", "http://x:1", "--key", "sk-abc", "llm", "hello world"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReorderAlreadyInOrderIsIdentity(t *testing.T) {
	fs := newRunLikeFlagSet()
	in := []string{"--listen", "http://x:1", "--key", "sk-abc", "llm", "hi"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %#v, want %#v (identity)", got, in)
	}
}

func TestReorderEqualsFormStaysOneToken(t *testing.T) {
	fs := newRunLikeFlagSet()
	in := []string{"llm", "hello", "--listen=http://x:1", "--key=sk-abc"}
	want := []string{"--listen=http://x:1", "--key=sk-abc", "llm", "hello"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReorderBoolFlagDoesNotEatNext(t *testing.T) {
	fs := newRunLikeFlagSet()
	// --verbose is bool; "hello" must stay positional, not be consumed as value.
	in := []string{"llm", "hello", "--verbose"}
	want := []string{"--verbose", "llm", "hello"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReorderDoubleDashStops(t *testing.T) {
	fs := newRunLikeFlagSet()
	// Everything after "--" is positional, even tokens that look like flags.
	in := []string{"--listen", "http://x:1", "llm", "--", "--this-is-the-prompt", "--flag-like"}
	want := []string{"--listen", "http://x:1", "llm", "--this-is-the-prompt", "--flag-like"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReorderBareDashIsPositional(t *testing.T) {
	fs := newRunLikeFlagSet()
	in := []string{"-", "llm", "--listen", "http://x:1"}
	want := []string{"--listen", "http://x:1", "-", "llm"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReorderUnknownFlagDoesNotEatNext(t *testing.T) {
	fs := newRunLikeFlagSet()
	// Unknown --bogus: keep moving as a single token, do NOT consume the next.
	// fs.Parse will then surface the "flag provided but not defined" error
	// rather than silently swallowing a positional.
	in := []string{"llm", "hello", "--bogus", "value-or-not"}
	want := []string{"--bogus", "llm", "hello", "value-or-not"}
	got := reorderForFlagSet(fs, in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestReorderParseEndToEnd(t *testing.T) {
	// Parsing the reordered output through the very flag set should yield
	// the same Args() the operator would expect.
	fs := newRunLikeFlagSet()
	listen := fs.Lookup("listen")
	key := fs.Lookup("key")

	in := []string{"llm", "hello world", "--listen", "http://x:1", "--key", "sk-abc"}
	args := reorderForFlagSet(fs, in)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if listen.Value.String() != "http://x:1" {
		t.Errorf("listen = %q", listen.Value.String())
	}
	if key.Value.String() != "sk-abc" {
		t.Errorf("key = %q", key.Value.String())
	}
	if got := fs.Args(); !reflect.DeepEqual(got, []string{"llm", "hello world"}) {
		t.Errorf("Args() = %#v", got)
	}
}

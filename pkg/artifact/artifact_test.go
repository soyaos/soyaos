package artifact

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestKindValid(t *testing.T) {
	for _, k := range []Kind{KindHTML, KindPDF, KindLongImage, KindMarkdown, KindXLSX, KindMP4} {
		if !k.Valid() {
			t.Errorf("%q should be valid", k)
		}
	}
	if Kind("svg").Valid() {
		t.Error("svg should not be in the v0.1 set")
	}
}

type fakeRenderer struct {
	kind Kind
	body string
}

func (f *fakeRenderer) Kind() Kind { return f.kind }
func (f *fakeRenderer) Render(_ context.Context, _ any, dst io.Writer) (Artifact, error) {
	n, err := io.Copy(dst, strings.NewReader(f.body))
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{Kind: f.kind, Size: n, Schema: "test.v1"}, nil
}

func TestRegistryRoundTrip(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeRenderer{kind: KindMarkdown, body: "hello"})

	if got := r.Kinds(); len(got) != 1 || got[0] != KindMarkdown {
		t.Fatalf("Kinds = %v, want [markdown]", got)
	}

	var buf strings.Builder
	a, err := r.Render(context.Background(), KindMarkdown, nil, &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if buf.String() != "hello" {
		t.Fatalf("rendered body = %q", buf.String())
	}
	if a.Size != 5 {
		t.Fatalf("Size = %d, want 5", a.Size)
	}
}

func TestRegistryUnknownKind(t *testing.T) {
	r := NewRegistry()
	_, err := r.Render(context.Background(), KindMP4, nil, io.Discard)
	if err == nil {
		t.Fatal("Render(unregistered) returned nil error")
	}
}

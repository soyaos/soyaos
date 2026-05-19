package kernel

import (
	"context"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/modelgw"
)

func TestKernel_RegisterAndChatCompletion_Echo(t *testing.T) {
	k := New()
	k.Register(EchoAgent)

	resp, err := k.ChatCompletion(context.Background(), auth.Identity{Subject: "local"}, modelgw.Request{
		Model:    "soya:echo",
		Messages: []modelgw.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if !strings.Contains(resp.Content, "echo: hello") {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	if resp.Model != "soya:echo" {
		t.Fatalf("response model = %q, want soya:echo", resp.Model)
	}
}

func TestKernel_LookupAcceptsBareSlug(t *testing.T) {
	k := New()
	k.Register(EchoAgent)
	if _, ok := k.Lookup("echo"); !ok {
		t.Fatal("bare slug 'echo' should resolve to 'soya:echo'")
	}
	if _, ok := k.Lookup("soya:echo"); !ok {
		t.Fatal("full id 'soya:echo' should resolve")
	}
	if _, ok := k.Lookup("nope"); ok {
		t.Fatal("'nope' should not resolve")
	}
}

func TestKernel_UnknownAgent(t *testing.T) {
	k := New()
	_, err := k.ChatCompletion(context.Background(), auth.Identity{}, modelgw.Request{Model: "soya:missing"})
	if err == nil {
		t.Fatal("ChatCompletion(unknown) returned nil error")
	}
}

func TestKernel_StreamFanout(t *testing.T) {
	k := New()
	k.Register(EchoAgent)

	out := make(chan modelgw.Chunk, 4)
	go func() {
		_ = k.ChatCompletionStream(context.Background(), auth.Identity{}, modelgw.Request{
			Model:    "soya:echo",
			Messages: []modelgw.Message{{Role: "user", Content: "ping"}},
		}, out)
		close(out)
	}()

	var sb strings.Builder
	sawDone := false
	for c := range out {
		if c.Done {
			sawDone = true
			break
		}
		sb.WriteString(c.Delta)
	}
	if !sawDone {
		t.Fatal("stream never emitted Done")
	}
	if !strings.Contains(sb.String(), "echo: ping") {
		t.Fatalf("stream content = %q", sb.String())
	}
}

package kernel_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"github.com/soyaos/soyaos/pkg/state"
)

func TestStatefulAgentPersistsWorkbookAndReloadsAuthoritativeRow(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStateStore()
	manifest := &soyapack.Manifest{
		Name:      "estate-muse",
		State:     &soyapack.StateDecl{Scope: "agent", Store: "kv"},
		Artifacts: []soyapack.ArtifactDecl{{Kind: "xlsx", Schema: "topics.v1"}},
		Actions:   []soyapack.ActionDecl{{ID: "generate_post", On: "per_row", Handler: "prompts/generate_post.md"}},
	}
	snapshot := "```json\n" + `{"sheets":[{"columns":[{"header":"标题"},{"header":"维度"},{"header":"切面"},{"header":"钩子"}],"rows":[["真实标题","market","数据","真实钩子"]]}]}` + "\n```"
	agent := kernel.Agent{
		Slug:     "estate-muse",
		Manifest: manifest,
		Handler: func(_ context.Context, _ auth.Identity, _ llmcall.Request, out chan<- llmcall.Chunk) error {
			out <- llmcall.Chunk{Delta: snapshot}
			out <- llmcall.Chunk{Done: true, FinishReason: "stop"}
			return nil
		},
	}

	first := kernel.New()
	first.SetStateStore(store)
	first.Register(agent)
	if _, err := first.ChatCompletion(ctx, auth.Identity{Subject: "editor-1"}, llmcall.Request{Model: agent.ModelID()}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	completion, err := store.Get(ctx, state.ScopeAgent, "estate-muse", kernel.CompletionStateKey)
	if err != nil {
		t.Fatalf("completion state: %v", err)
	}
	if string(completion.Value) != snapshot {
		t.Fatalf("completion = %q, want original response", completion.Value)
	}
	if _, err := store.Get(ctx, state.ScopeAgent, "estate-muse", "artifact/topics.v1/latest"); err != nil {
		t.Fatalf("artifact state: %v", err)
	}

	// A fresh Kernel simulates a process restart. It receives a tampered
	// caller payload, but the persisted workbook row must win.
	restarted := kernel.New()
	restarted.SetStateStore(store)
	restarted.Register(agent)
	var got map[string]any
	restarted.SetActionHandler(func(_ context.Context, _ soyapack.ActionDecl, req kernel.ActionRequest) (kernel.ActionResult, error) {
		got = req.Payload
		return kernel.ActionResult{Status: "done"}, nil
	})
	if _, err := restarted.InvokeAction(ctx, auth.Identity{Subject: "editor-1"}, "estate-muse", "generate_post", "row-1", map[string]any{
		"title":  "伪造标题",
		"option": "保留这个动作选项",
	}); err != nil {
		t.Fatalf("InvokeAction after restart: %v", err)
	}
	if got["title"] != "真实标题" || got["标题"] != "真实标题" {
		t.Fatalf("stored row was not authoritative: %#v", got)
	}
	if got["option"] != "保留这个动作选项" {
		t.Fatalf("action-specific caller option was lost: %#v", got)
	}
}

type memoryStateStore struct {
	mu      sync.Mutex
	entries map[string]state.Entry
}

func newMemoryStateStore() *memoryStateStore {
	return &memoryStateStore{entries: map[string]state.Entry{}}
}

func stateKey(scope state.Scope, owner, key string) string {
	return string(scope) + "\x00" + owner + "\x00" + key
}

func (s *memoryStateStore) Get(_ context.Context, scope state.Scope, owner, key string) (state.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[stateKey(scope, owner, key)]
	if !ok {
		return state.Entry{}, state.ErrNotFound
	}
	entry.Value = append([]byte(nil), entry.Value...)
	return entry, nil
}

func (s *memoryStateStore) Put(_ context.Context, scope state.Scope, owner, key string, value []byte) (state.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mapKey := stateKey(scope, owner, key)
	version := s.entries[mapKey].Version + 1
	entry := state.Entry{Scope: scope, OwnerID: owner, Key: key, Value: append([]byte(nil), value...), Version: version, UpdatedAt: time.Now()}
	s.entries[mapKey] = entry
	return entry, nil
}

func (s *memoryStateStore) CompareAndSwap(ctx context.Context, scope state.Scope, owner, key string, version int64, value []byte) (state.Entry, error) {
	current, err := s.Get(ctx, scope, owner, key)
	if err != nil && !(errors.Is(err, state.ErrNotFound) && version == 0) {
		return state.Entry{}, err
	}
	if current.Version != version {
		return state.Entry{}, state.ErrConflict
	}
	return s.Put(ctx, scope, owner, key, value)
}

func (s *memoryStateStore) Delete(_ context.Context, scope state.Scope, owner, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, stateKey(scope, owner, key))
	return nil
}

func (s *memoryStateStore) List(_ context.Context, scope state.Scope, owner, prefix string) ([]state.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []state.Entry
	for _, entry := range s.entries {
		if entry.Scope == scope && entry.OwnerID == owner && strings.HasPrefix(entry.Key, prefix) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

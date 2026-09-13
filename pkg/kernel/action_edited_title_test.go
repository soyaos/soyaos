package kernel_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/kernel"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"github.com/soyaos/soyaos/pkg/state"
)

func TestActionEditedTitleIsLocalAndPreservesStoredContext(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStateStore()
	stored := map[string]any{
		"title": "原始选题", "标题": "原始选题", "topic": "原始选题",
		"original_request": "杭州，1000万，新房和二手房，小红书获客，不虚构事实",
		"dimension":        "物业", "angle": "维护", "hook": "观察响应",
		"edited_title": "历史字段不能替代本次编辑",
	}
	body, _ := json.Marshal(stored)
	before, err := s.Put(ctx, state.ScopeRow, "estate-muse/row-1", "payload", body)
	if err != nil {
		t.Fatal(err)
	}
	k := editedTitleKernel(s)
	var got map[string]any
	k.SetActionHandler(func(_ context.Context, _ soyapack.ActionDecl, req kernel.ActionRequest) (kernel.ActionResult, error) {
		got = req.Payload
		return kernel.ActionResult{Status: "done"}, nil
	})
	edited := "入住后如何评估出来物业对公共区域维护的响应速度？"
	payload := map[string]any{
		"edited_title": "  " + edited + " \n", "title": "隐式伪造", "标题": "另一个标题",
		"original_title": "伪造原题", "original_request": "上海", "dimension": "伪造",
		"angle": "伪造", "hook": "伪造", "option": "保留选项",
	}
	callerBefore, _ := json.Marshal(payload)
	if _, err := k.InvokeAction(ctx, auth.Identity{}, "estate-muse", "generate_post", "row-1", payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"title", "标题", "topic", "edited_title"} {
		if got[key] != edited {
			t.Errorf("%s = %#v", key, got[key])
		}
	}
	if got["original_title"] != stored["title"] || got["option"] != "保留选项" {
		t.Fatalf("incorrect provenance/options: %#v", got)
	}
	for _, key := range []string{"original_request", "dimension", "angle", "hook"} {
		if got[key] != stored[key] {
			t.Errorf("stored %s overwritten: %#v", key, got[key])
		}
	}
	callerAfter, _ := json.Marshal(payload)
	if string(callerBefore) != string(callerAfter) {
		t.Fatal("caller map mutated")
	}
	after, err := s.Get(ctx, state.ScopeRow, "estate-muse/row-1", "payload")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("persisted row changed: %v", err)
	}
	// Later invocations without an explicit edit still use the original row.
	if _, err := k.InvokeAction(ctx, auth.Identity{}, "estate-muse", "generate_post", "row-1", map[string]any{"title": "隐式替换"}); err != nil {
		t.Fatal(err)
	}
	if got["title"] != stored["title"] || got["标题"] != stored["标题"] {
		t.Fatalf("edit leaked into later invocation: %#v", got)
	}
}

func editedTitleKernel(s state.Store) *kernel.Kernel {
	k := kernel.New()
	k.SetStateStore(s)
	k.Register(kernel.Agent{Slug: "estate-muse", Manifest: &soyapack.Manifest{
		Name: "estate-muse", State: &soyapack.StateDecl{Scope: "agent", Store: "kv"},
		Actions: []soyapack.ActionDecl{{ID: "generate_post"}},
	}})
	return k
}

func TestActionEditedTitleValidation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		edited, stored any
		exists, valid  bool
	}{
		{"empty", "", "原题", true, false},
		{"whitespace", " \n\t　", "原题", true, false},
		{"number", 42, "原题", true, false},
		{"null", nil, "原题", true, false},
		{"object", map[string]any{"title": "新题"}, "原题", true, false},
		{"too long", strings.Repeat("中", 501), "原题", true, false},
		{"invalid utf8", string([]byte{0xff}), "原题", true, false},
		{"no row", "新题", nil, false, false},
		{"missing title", "新题", nil, true, false},
		{"invalid stored title", "新题", 42, true, false},
		{"blank stored title", "新题", "　", true, false},
		{"boundary", strings.Repeat("中", 500), "原题", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newMemoryStateStore()
			if tc.exists {
				body, _ := json.Marshal(map[string]any{"title": tc.stored})
				if _, err := s.Put(context.Background(), state.ScopeRow, "estate-muse/row-1", "payload", body); err != nil {
					t.Fatal(err)
				}
			}
			k := editedTitleKernel(s)
			called := false
			k.SetActionHandler(func(_ context.Context, _ soyapack.ActionDecl, _ kernel.ActionRequest) (kernel.ActionResult, error) {
				called = true
				return kernel.ActionResult{}, nil
			})
			_, err := k.InvokeAction(context.Background(), auth.Identity{}, "estate-muse", "generate_post", "row-1", map[string]any{"edited_title": tc.edited})
			if tc.valid {
				if err != nil || !called {
					t.Fatalf("valid edit failed: %v", err)
				}
			} else if !errors.Is(err, kernel.ErrInvalidActionPayload) || called {
				t.Fatalf("invalid edit dispatched: called=%v err=%v", called, err)
			}
		})
	}
}

func TestActionWithoutEditedTitleRetainsStatelessLegacyPayload(t *testing.T) {
	k := editedTitleKernel(nil)
	for _, payload := range []map[string]any{nil, {"title": "legacy title", "option": true}} {
		k.SetActionHandler(func(_ context.Context, _ soyapack.ActionDecl, req kernel.ActionRequest) (kernel.ActionResult, error) {
			if !reflect.DeepEqual(req.Payload, payload) {
				t.Fatalf("legacy payload changed: %#v", req.Payload)
			}
			return kernel.ActionResult{}, nil
		})
		if _, err := k.InvokeAction(context.Background(), auth.Identity{}, "estate-muse", "generate_post", "row-1", payload); err != nil {
			t.Fatal(err)
		}
	}
}

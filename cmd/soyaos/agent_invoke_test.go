package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdAgentInvokeExpectedRows(t *testing.T) {
	for _, count := range []int{122, 499, 500, 501} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			rows := make([][]any, count)
			for i := range rows {
				rows[i] = []any{fmt.Sprintf("Topic %d", i+1)}
			}
			snapshot, _ := json.Marshal(map[string]any{"sheets": []any{map[string]any{"name": "Topics", "columns": []any{map[string]any{"header": "Topic"}}, "rows": rows}}})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(snapshot)}}}})
			}))
			defer server.Close()
			output := filepath.Join(t.TempDir(), "topics.xlsx")
			if err := os.WriteFile(output, []byte("existing workbook"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"estate-muse", "test", "--listen", server.URL, "--artifact", "xlsx", "--output", output}
			err := cmdAgentInvoke(append(args, "--expected-rows", "500"))
			if count == 500 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "expected 500 primary data rows") {
					t.Fatalf("err=%v", err)
				}
				got, _ := os.ReadFile(output)
				if string(got) != "existing workbook" {
					t.Fatal("rejected generation overwrote existing workbook")
				}
			}
			if err := cmdAgentInvoke(args); err != nil {
				t.Fatalf("default compatibility: %v", err)
			}
		})
	}
}

func TestExpectedRowsRejectsInvalidFlags(t *testing.T) {
	for _, flags := range [][]string{
		{"--expected-rows=0", "--artifact=xlsx"}, {"--expected-rows=-1", "--artifact=xlsx"},
		{"--expected-rows=abc", "--artifact=xlsx"}, {"--expected-rows=1.5", "--artifact=xlsx"},
		{"--expected-rows=500"}, {"--expected-rows=500", "--artifact=mp4"},
	} {
		if err := cmdAgentInvoke(append([]string{"estate-muse", "test"}, flags...)); err == nil || !strings.Contains(err.Error(), "expected-rows") {
			t.Fatalf("flags=%v err=%v", flags, err)
		}
	}
}

func TestExpectedRowsRejectsPaddingAndSupplementaryRowsBeforeCreatingOutput(t *testing.T) {
	for _, body := range []string{
		`{"sheets":[{"columns":[{"header":"Topic"}],"rows":[["valid"],[]]}]}`,
		`{"sheets":[{"columns":[{"header":"Topic"}],"rows":[["valid"],[null]]}]}`,
		`{"sheets":[{"columns":[{"header":"Topic"}],"rows":[["valid"],["  "]]}]}`,
		`{"sheets":[{"columns":[{"header":"Topic"}],"rows":[["valid"],"malformed"]}]}`,
		`{"sheets":[{"columns":[{"header":"Topic"}],"rows":[["valid"]]},{"columns":[{"header":"Other"}],"rows":[["other"]]}]}`,
	} {
		output := filepath.Join(t.TempDir(), "not-created", "topics.xlsx")
		if err := renderXLSXArtifact(body, "topics.v1", output, 2); err == nil {
			t.Fatalf("accepted invalid rows: %s", body)
		}
		if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
			t.Fatalf("created output directory before validation: %v", err)
		}
	}
}

func TestCmdAgentInvokeMaxTokens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  float64
	}{
		{name: "default omitted"},
		{name: "explicit", flags: []string{"--max-tokens", "65536"}, want: 65536},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer server.Close()
			args := append([]string{"estate-muse", "test prompt", "--listen", server.URL}, tc.flags...)
			if err := cmdAgentInvoke(args); err != nil {
				t.Fatal(err)
			}
			got, present := payload["max_tokens"]
			if tc.want == 0 && present {
				t.Fatalf("default must omit max_tokens, got %v", got)
			}
			if tc.want > 0 && got != tc.want {
				t.Fatalf("max_tokens=%v want=%v", got, tc.want)
			}
			if payload["model"] != "soya:estate-muse" {
				t.Fatalf("model=%v", payload["model"])
			}
		})
	}
}

func TestCmdAgentInvokeRejectsNonPositiveMaxTokens(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		err := cmdAgentInvoke([]string{"estate-muse", "test", "--max-tokens=" + value})
		if err == nil || !strings.Contains(err.Error(), "--max-tokens must be positive") {
			t.Fatalf("value=%s err=%v", value, err)
		}
	}
}

func TestStripJSONFence(t *testing.T) {
	input := "```json\n{\"remotion\":{\"props\":{}}}\n```\n"
	if got, want := stripJSONFence(input), `{"remotion":{"props":{}}}`; got != want {
		t.Fatalf("stripJSONFence=%q, want %q", got, want)
	}
}

func TestChunkReaderPreservesBytes(t *testing.T) {
	chunks := make(chan []byte, 3)
	chunks <- []byte("abc")
	chunks <- []byte("defgh")
	close(chunks)
	got, err := io.ReadAll(channelReader(chunks))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("abcdefgh")) {
		t.Fatalf("got=%q", got)
	}
}

func TestCopyPortableRemotionProject(t *testing.T) {
	src := filepath.Join(t.TempDir(), "project")
	dst := filepath.Join(t.TempDir(), "export")
	for _, dir := range []string{filepath.Join(src, "src"), filepath.Join(src, "node_modules", "pkg"), filepath.Join(src, "out")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		filepath.Join(src, "package.json"):                    `{}`,
		filepath.Join(src, "bun.lock"):                        "lock",
		filepath.Join(src, "src", "index.ts"):                 "export {};",
		filepath.Join(src, "node_modules", "pkg", "index.js"): "noise",
		filepath.Join(src, "out", "clip.mp4"):                 "noise",
	} {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	props := []byte(`{"duration_seconds":30}`)
	if err := copyPortableRemotionProject(src, dst, props); err != nil {
		t.Fatalf("copyPortableRemotionProject: %v", err)
	}
	for _, name := range []string{"package.json", "bun.lock", filepath.Join("src", "index.ts"), "props.json"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	for _, name := range []string{"node_modules", "out"} {
		if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Errorf("excluded %s was copied", name)
		}
	}
}

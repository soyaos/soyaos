package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/soyaos/soyaos/pkg/auth"
	"github.com/soyaos/soyaos/pkg/state"
)

const (
	// CompletionStateKey is the stable key holding a Stateful Agent's most
	// recent successful chat response.
	CompletionStateKey = "completion/latest"
	rowPayloadStateKey = "payload"
)

// SetStateStore wires Stateful Agent persistence. The caller owns the store's
// lifecycle; Kernel never closes it.
func (k *Kernel) SetStateStore(s state.Store) {
	k.stateMu.Lock()
	defer k.stateMu.Unlock()
	k.stateStore = s
}

func (k *Kernel) getStateStore() state.Store {
	k.stateMu.RLock()
	defer k.stateMu.RUnlock()
	return k.stateStore
}

func (k *Kernel) persistAgentCompletion(ctx context.Context, agent Agent, id auth.Identity, content string) error {
	s := k.getStateStore()
	if s == nil || agent.Manifest == nil || agent.Manifest.State == nil {
		return nil
	}
	scope, owner := completionStateOwner(agent, id)
	if _, err := s.Put(ctx, scope, owner, CompletionStateKey, []byte(content)); err != nil {
		return fmt.Errorf("kernel: persist %s completion: %w", agent.Slug, err)
	}

	snapshotBody, ok := extractJSONObject(content)
	if !ok {
		return nil
	}
	for _, decl := range agent.Manifest.Artifacts {
		if decl.Kind != "xlsx" {
			continue
		}
		if _, err := s.Put(ctx, scope, owner, "artifact/"+decl.Schema+"/latest", snapshotBody); err != nil {
			return fmt.Errorf("kernel: persist %s artifact %s: %w", agent.Slug, decl.Schema, err)
		}
		if err := persistWorkbookRows(ctx, s, agent, id, snapshotBody); err != nil {
			return err
		}
		break
	}
	return nil
}

func completionStateOwner(agent Agent, id auth.Identity) (state.Scope, string) {
	if agent.Manifest != nil && agent.Manifest.State != nil && agent.Manifest.State.Scope == "user" {
		owner := id.Subject
		if owner == "" {
			owner = id.KeyID
		}
		if owner == "" {
			owner = "anonymous"
		}
		return state.ScopeUser, agent.Slug + "/" + owner
	}
	return state.ScopeAgent, agent.Slug
}

type workbookSnapshot struct {
	Sheets []struct {
		Columns []struct {
			Header string `json:"header"`
		} `json:"columns"`
		Rows [][]any `json:"rows"`
	} `json:"sheets"`
}

func persistWorkbookRows(ctx context.Context, s state.Store, agent Agent, id auth.Identity, body []byte) error {
	var snapshot workbookSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return fmt.Errorf("kernel: decode %s xlsx snapshot: %w", agent.Slug, err)
	}
	for _, sheet := range snapshot.Sheets {
		for rowIndex, row := range sheet.Rows {
			rowID := "row-" + strconv.Itoa(rowIndex+1)
			payload := map[string]any{"row_id": rowID}
			for columnIndex, value := range row {
				if columnIndex >= len(sheet.Columns) {
					break
				}
				header := strings.TrimSpace(sheet.Columns[columnIndex].Header)
				if header == "" {
					continue
				}
				payload[header] = value
				if alias := canonicalRowField(header); alias != "" {
					payload[alias] = value
				}
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("kernel: encode %s %s: %w", agent.Slug, rowID, err)
			}
			if _, err := s.Put(ctx, state.ScopeRow, rowStateOwner(agent, id, rowID), rowPayloadStateKey, encoded); err != nil {
				return fmt.Errorf("kernel: persist %s %s: %w", agent.Slug, rowID, err)
			}
		}
	}
	return nil
}

func canonicalRowField(header string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "标题", "topic", "title":
		return "title"
	case "维度", "dimension":
		return "dimension"
	case "切面", "angle":
		return "angle"
	case "钩子", "hook":
		return "hook"
	case "难度", "difficulty":
		return "difficulty"
	case "建议产物", "recommended artifact", "recommended_artifact":
		return "recommended_artifact"
	default:
		return ""
	}
}

func rowStateOwner(agent Agent, id auth.Identity, rowID string) string {
	_, completionOwner := completionStateOwner(agent, id)
	return completionOwner + "/" + rowID
}

func (k *Kernel) loadRowPayload(ctx context.Context, agent Agent, id auth.Identity, rowID string) (map[string]any, error) {
	s := k.getStateStore()
	if s == nil || agent.Manifest == nil || agent.Manifest.State == nil {
		return nil, nil
	}
	entry, err := s.Get(ctx, state.ScopeRow, rowStateOwner(agent, id, rowID), rowPayloadStateKey)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kernel: load %s %s: %w", agent.Slug, rowID, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(entry.Value, &payload); err != nil {
		return nil, fmt.Errorf("kernel: decode %s %s: %w", agent.Slug, rowID, err)
	}
	return payload, nil
}

func extractJSONObject(content string) ([]byte, bool) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		if newline := strings.IndexByte(trimmed, '\n'); newline >= 0 {
			trimmed = trimmed[newline+1:]
		}
		trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), "```"))
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, false
	}
	return []byte(trimmed), true
}

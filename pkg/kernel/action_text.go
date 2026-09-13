package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
)

func validateActionText(content string, cfg *soyapack.TextValidation) error {
	if cfg == nil {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	for _, phrase := range cfg.ForbiddenPhrases {
		if strings.Contains(content, phrase) {
			return fmt.Errorf("remove unsupported phrase %q; use verification questions, not unsupported assertions", phrase)
		}
	}
	body := content
	if cfg.Section != "" {
		var lines []string
		active, found := false, false
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				if active {
					break
				}
				if strings.Contains(strings.TrimLeft(trimmed, "# "), cfg.Section) {
					active = true
					found = true
				}
				continue
			}
			if active {
				lines = append(lines, line)
			}
		}
		if !found {
			return fmt.Errorf("missing Markdown heading for section %q", cfg.Section)
		}
		body = strings.Join(lines, "\n")
	}
	count := 0
	for _, r := range body {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			count++
		}
	}
	if count < cfg.MinChars || count > cfg.MaxChars {
		if count > cfg.MaxChars {
			target := (cfg.MinChars + cfg.MaxChars) / 2
			return fmt.Errorf("section %q has %d letters/numbers, require %d..%d; delete about %d counted characters to reach %d, retaining about %d%% of the failed draft; shorten whole paragraphs and lists instead of merely changing individual words; do not self-report a word count", cfg.Section, count, cfg.MinChars, cfg.MaxChars, count-target, target, target*100/count)
		}
		return fmt.Errorf("section %q has %d letters/numbers, require %d..%d; rewrite toward %d actual letters/numbers to leave margin; do not self-report a word count", cfg.Section, count, cfg.MinChars, cfg.MaxChars, (cfg.MinChars+cfg.MaxChars)/2)
	}
	if cfg.MirrorTable != nil {
		return validateActionMirrorTable(content, body, cfg.MirrorTable)
	}
	return nil
}

func collectValidatedAction(ctx context.Context, provider llmcall.Provider, decl soyapack.ActionDecl, original string, req llmcall.Request, reviewPrompts ...string) (string, error) {
	var drafting map[string]json.RawMessage
	if len(req.Messages) > 1 {
		_ = json.Unmarshal([]byte(req.Messages[1].Content), &drafting)
	}
	repairs := 0
	if decl.TextValidation != nil {
		if err := decl.TextValidation.Validate(); err != nil {
			return "", err
		}
		repairs = decl.TextValidation.MaxRepairs
	}
	for attempt := 0; ; attempt++ {
		content, err := streamCollect(ctx, provider, req)
		if err != nil {
			return "", err
		}
		validationErr := validateActionText(content, decl.TextValidation)
		if len(reviewPrompts) > 0 && reviewPrompts[0] != "" {
			// Collect both kinds of feedback before spending the shared repair.
			// Otherwise a length-only repair can exhaust the budget before the
			// generator is ever told about an unsupported inference.
			semanticErr, err := reviewActionText(ctx, provider, req.Model, reviewPrompts[0], original, content, decl.ID)
			if err != nil {
				return "", err
			}
			validationErr = errors.Join(validationErr, semanticErr)
		}
		if validationErr == nil {
			return content, nil
		}
		if attempt >= repairs {
			return "", validationErr
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(original), &payload); err != nil {
			return "", err
		}
		payload["repair"] = true
		if plan, ok := drafting["editorial_plan"]; ok {
			payload["editorial_plan"] = plan
		}
		payload["previous_output"] = content
		payload["validation_error"] = validationErr.Error()
		data, _ := json.Marshal(payload)
		system := req.Messages[0]
		system.Content += "\nThis is a bounded repair of a failed draft. Preserve the original business constraints and required format, but do not preserve the draft's length or unsupported claims. Apply validation_error to previous_output; return only the corrected final content."
		req.Messages = []llmcall.Message{system, {Role: "user", Content: string(data)}}
	}
}

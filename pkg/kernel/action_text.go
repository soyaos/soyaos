package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/soyaos/soyaos/pkg/llmcall"
	"github.com/soyaos/soyaos/pkg/soyapack"
	"strings"
	"unicode"
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
		return fmt.Errorf("section %q has %d letters/numbers, require %d..%d; rewrite toward %d actual letters/numbers to leave margin; do not self-report a word count", cfg.Section, count, cfg.MinChars, cfg.MaxChars, (cfg.MinChars+cfg.MaxChars)/2)
	}
	return nil
}

func collectValidatedAction(ctx context.Context, provider llmcall.Provider, decl soyapack.ActionDecl, original string, req llmcall.Request) (string, error) {
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
		payload["previous_output"] = content
		payload["validation_error"] = validationErr.Error()
		data, _ := json.Marshal(payload)
		req.Messages = []llmcall.Message{req.Messages[0], {Role: "user", Content: string(data)}}
	}
}

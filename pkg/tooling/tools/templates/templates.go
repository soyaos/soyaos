// Package templates is the SoyaOS multi-platform draft template pool
// (EstateMuse Aha #5: "Compose once, repurpose for every Chinese social
// platform"). The four canonical templates target WeChat 公众号, 抖音
// 口播脚本, 视频号文案, and 小红书爆款体. Each template carries a YAML-ish
// frontmatter declaring min/max length, banned words, and style keywords
// so the originality precheck (APP-504) and downstream policy gates can
// reason about constraints without parsing the prose.
//
// Templates are embedded into the binary via go:embed so any operator
// running soyaos can mint a draft without pulling extra files.
package templates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

//go:embed *.tmpl.md
var templateFS embed.FS

// TemplateID enumerates the canonical templates shipped in this pool.
type TemplateID string

const (
	WeChatPost          TemplateID = "wechat_post"
	DouyinCaption       TemplateID = "douyin_caption"
	VideoAccountCaption TemplateID = "video_account_caption"
	XiaohongshuPost     TemplateID = "xiaohongshu_post"
)

// All returns every TemplateID that ships in this pool.
func All() []TemplateID {
	return []TemplateID{WeChatPost, DouyinCaption, VideoAccountCaption, XiaohongshuPost}
}

// Frontmatter captures the structured rules at the top of each template
// file. Length is in Chinese characters (we approximate with rune count).
type Frontmatter struct {
	Platform      string   `json:"platform"`
	MinLen        int      `json:"min_len"`
	MaxLen        int      `json:"max_len"`
	BannedWords   []string `json:"banned_words"`
	StyleKeywords []string `json:"style_keywords"`
}

// Template is one entry in the pool: parsed frontmatter + the raw body
// the LLM is meant to fill in.
type Template struct {
	ID          TemplateID
	Frontmatter Frontmatter
	Body        string // the prompt body after the frontmatter block
	Raw         string // original file contents (frontmatter + body)
}

// ErrUnknownTemplate is returned when LoadTemplate is asked for an id
// that doesn't ship in the pool.
var ErrUnknownTemplate = errors.New("templates: unknown template id")

// LoadTemplate reads one template from the embedded FS and parses its
// frontmatter.
func LoadTemplate(id TemplateID) (Template, error) {
	body, err := fs.ReadFile(templateFS, string(id)+".tmpl.md")
	if err != nil {
		return Template{}, fmt.Errorf("%w: %s", ErrUnknownTemplate, id)
	}
	tmpl, err := parseTemplate(id, string(body))
	if err != nil {
		return Template{}, err
	}
	return tmpl, nil
}

// LoadAll loads every template in the pool. Order matches All().
func LoadAll() ([]Template, error) {
	ids := All()
	out := make([]Template, 0, len(ids))
	for _, id := range ids {
		t, err := LoadTemplate(id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// parseTemplate splits a file into frontmatter and body. The frontmatter
// is fenced by `---` lines at the top of the file. Inside the
// frontmatter, simple `key: value` lines are accepted; list values use
// the inline `[a, b, c]` form so we don't have to depend on a YAML
// parser for the four canonical templates.
func parseTemplate(id TemplateID, raw string) (Template, error) {
	tmpl := Template{ID: id, Raw: raw}
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return tmpl, fmt.Errorf("templates: %s: missing frontmatter fence at line 1", id)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return tmpl, fmt.Errorf("templates: %s: unterminated frontmatter", id)
	}
	fm, err := parseFrontmatter(lines[1:end])
	if err != nil {
		return tmpl, fmt.Errorf("templates: %s: %w", id, err)
	}
	tmpl.Frontmatter = fm
	tmpl.Body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return tmpl, nil
}

func parseFrontmatter(lines []string) (Frontmatter, error) {
	fm := Frontmatter{}
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		key, val, ok := strings.Cut(ln, ":")
		if !ok {
			return fm, fmt.Errorf("malformed frontmatter line %q", ln)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "platform":
			fm.Platform = trimQuotes(val)
		case "min_len":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fm, fmt.Errorf("min_len not an int: %v", err)
			}
			fm.MinLen = n
		case "max_len":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fm, fmt.Errorf("max_len not an int: %v", err)
			}
			fm.MaxLen = n
		case "banned_words":
			fm.BannedWords = parseInlineList(val)
		case "style_keywords":
			fm.StyleKeywords = parseInlineList(val)
		}
	}
	return fm, nil
}

func parseInlineList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = trimQuotes(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

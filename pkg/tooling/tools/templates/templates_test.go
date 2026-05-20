// templates_test.go asserts every shipped template loads, has a
// non-empty body, and carries the frontmatter fields the originality
// precheck (APP-504) consumes (min_len / max_len / banned_words).
package templates

import (
	"strings"
	"testing"
)

func TestLoadAll_AllFourTemplates(t *testing.T) {
	tmpls, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(tmpls) != 4 {
		t.Fatalf("LoadAll returned %d templates, want 4", len(tmpls))
	}
	ids := map[TemplateID]bool{}
	for _, tmpl := range tmpls {
		ids[tmpl.ID] = true
		if tmpl.Frontmatter.Platform == "" {
			t.Errorf("%s: empty platform", tmpl.ID)
		}
		if tmpl.Frontmatter.MinLen <= 0 || tmpl.Frontmatter.MaxLen <= 0 {
			t.Errorf("%s: min/max len missing (%d/%d)", tmpl.ID, tmpl.Frontmatter.MinLen, tmpl.Frontmatter.MaxLen)
		}
		if tmpl.Frontmatter.MaxLen <= tmpl.Frontmatter.MinLen {
			t.Errorf("%s: max_len <= min_len", tmpl.ID)
		}
		if len(tmpl.Frontmatter.BannedWords) == 0 {
			t.Errorf("%s: banned_words empty", tmpl.ID)
		}
		if !strings.Contains(tmpl.Body, "{{ .Source }}") {
			t.Errorf("%s: body lacks {{ .Source }} slot", tmpl.ID)
		}
	}
	for _, want := range []TemplateID{WeChatPost, DouyinCaption, VideoAccountCaption, XiaohongshuPost} {
		if !ids[want] {
			t.Errorf("missing template %s", want)
		}
	}
}

func TestLoadTemplate_UnknownID(t *testing.T) {
	if _, err := LoadTemplate("nope"); err == nil {
		t.Fatal("expected error for unknown template id")
	}
}

func TestWeChatPost_BannedWordsParsedAsList(t *testing.T) {
	tmpl, err := LoadTemplate(WeChatPost)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range tmpl.Frontmatter.BannedWords {
		if w == "包治百病" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected '包治百病' in banned words, got %+v", tmpl.Frontmatter.BannedWords)
	}
}

func TestXiaohongshuPost_StyleKeywordsParsed(t *testing.T) {
	tmpl, err := LoadTemplate(XiaohongshuPost)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Frontmatter.StyleKeywords) == 0 {
		t.Fatal("xiaohongshu_post style_keywords empty")
	}
}

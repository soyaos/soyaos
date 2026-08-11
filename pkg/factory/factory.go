// Package factory is the Agent Factory — natural language → SoyaPack
// manifest translation (DD-009, NewsBeam).
//
// The factory is itself an Agent in the SoyaOS conceptual model, but it
// boots statically inside the kernel to break the "chicken and egg"
// dependency: without the factory there is no way to create the first
// Agent. v0.1.0-alpha exposes the translator interface plus a
// heuristic-first Translate implementation. The LLM seam (TranslateLLM)
// is injected so production callers can plug in a real OpenAI-Compat
// client while tests stay deterministic.
//
// Architectural intent (locked by APP-460):
//
//   - pkg/soyapack — manifest *schema* (Skill / Agent / Memory) + loader +
//     validator. Owned by spec.
//   - pkg/factory  — *behavior*: turn a natural-language sentence into a
//     soyapack.Manifest{Kind: Agent, ...} value. Owned by Agent Factory.
//
// Splitting them means a manifest written by hand and a manifest produced
// by the Factory pass through the same validator.
package factory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/soyaos/soyaos/pkg/soyapack"
	"gopkg.in/yaml.v3"
)

// Translator converts a natural-language description of an Agent into a
// SoyaPack v0 Agent manifest. Implementations:
//
//   - Factory (production / default) — heuristic extractor, optionally
//     escalated to an LLM via the injected TranslateLLM.
//   - Stub (legacy) — kept for callers wired against the old shell;
//     returns ErrNotImplemented.
//
// All implementations must produce manifests that pass soyapack.Validate.
type Translator interface {
	Translate(ctx context.Context, nl string, locale string) (*soyapack.Manifest, error)
}

// TranslateLLM abstracts the LLM call site so the Factory can be tested
// without a network hop. The expected return is a YAML document encoding
// a soyapack.Manifest; the Factory unmarshals + validates the result and,
// on any failure, falls back to the deterministic heuristic path.
//
// In production this is satisfied by pkg/openaicompat (or an MCP tool
// proxy). In tests, callers inject a fake that returns a fixed YAML
// payload.
type TranslateLLM interface {
	Generate(ctx context.Context, system, user string) (string, error)
}

// Factory is the concrete Translator used by the kernel. With LLM==nil it
// runs in heuristic-only mode, which is good enough for the NewsBeam
// alpha demo ("每天早上 9 点把 AI 资讯发到钉钉群"). With a non-nil LLM
// the Factory still seeds the prompt with the heuristic manifest so a
// flaky LLM never regresses below the deterministic baseline.
type Factory struct {
	// LLM is the optional refinement seam. nil → heuristic-only.
	LLM TranslateLLM
}

// ErrNotImplemented is retained for backward compatibility with the
// alpha shell. It is no longer returned by Factory.Translate, only by
// the Stub Translator below.
var ErrNotImplemented = errors.New("factory: NL→manifest translator not implemented in v0.1.0-alpha.0 (see APP-492)")

// Stub is a placeholder Translator preserved so any pre-APP-492 wiring
// that still depends on it keeps compiling. New code should use Factory.
type Stub struct{}

// Translate always returns ErrNotImplemented.
func (Stub) Translate(_ context.Context, _ string, _ string) (*soyapack.Manifest, error) {
	return nil, ErrNotImplemented
}

// Translate converts nl into a soyapack.Manifest. locale is taken as a
// hint for the heuristic ("zh-CN" tilts toward 中文 keywords, "en" toward
// English) but the heuristic is intentionally bilingual — getting the
// hint wrong only changes which keyword set is preferred.
//
// Pipeline:
//
//  1. Extract slots from nl (intent / cadence / channel / source_hint).
//  2. Assemble a minimum-valid soyapack.Manifest{Kind: Agent}.
//  3. If f.LLM is non-nil, ask the LLM to refine the manifest. If the
//     LLM call or YAML unmarshal fails, fall back to the heuristic
//     result; we never bubble a network error up to the caller.
//  4. Validate the final manifest with soyapack.Validate.
func (f *Factory) Translate(ctx context.Context, nl, locale string) (*soyapack.Manifest, error) {
	nl = strings.TrimSpace(nl)
	if nl == "" {
		return nil, errors.New("factory: empty natural-language input")
	}

	s := extractSlots(nl, locale)
	manifest := assembleManifest(nl, s)

	if f.LLM != nil {
		if refined, err := f.refineWithLLM(ctx, nl, locale, manifest); err == nil && refined != nil {
			manifest = refined
		}
	}

	if err := soyapack.Validate(manifest); err != nil {
		return nil, fmt.Errorf("factory: manifest failed validation: %w", err)
	}
	return manifest, nil
}

// slots is the intermediate representation between the raw NL string and
// the final Manifest. Keeping it as a plain struct (no yaml tags) makes
// it cheap to log when debugging heuristic misses.
type slots struct {
	Intent     string // free-form summary
	Cron       string // 5-field cron expression, empty if none extracted
	Channel    string // "dingtalk" / "feishu" / "" — connectors.Kind values
	SourceHint string // RSS URL guess, empty if none
	UsesRSS    bool
	Locale     string // normalized locale tag ("zh-CN" / "en")
}

// --- heuristic extraction ---------------------------------------------------

// reHour catches "9 点" / "9:00" / "9am" / "9 AM" / "9:30". Captures the
// hour (and optionally the minute) in named groups.
var reHour = regexp.MustCompile(`(?i)(?P<h>\d{1,2})\s*(?:[:点](?P<m>\d{1,2})?\s*)?(?:am|AM|早上|上午)?`)

// keyword tables driven by extractSlots.
var (
	dailyMarkers   = []string{"每天", "每日", "daily", "every day", "每个早晨", "every morning"}
	morningMarkers = []string{"早上", "上午", "morning", " am"}
	dingtalkMarks  = []string{"钉钉", "dingtalk", "ding talk"}
	feishuMarks    = []string{"飞书", "feishu", "lark"}
	aiNewsMarks    = []string{"ai 资讯", "ai资讯", "ai news", "人工智能资讯", "ai 新闻", "ai新闻"}
	newsMarks      = []string{"资讯", "新闻", "news"}
)

func extractSlots(nl, locale string) slots {
	lc := strings.ToLower(nl)
	out := slots{Intent: nl, Locale: normalizeLocale(locale)}

	// --- cadence ---
	daily := containsAny(lc, dailyMarkers)
	morning := containsAny(lc, morningMarkers)

	if hour, minute, ok := extractClock(nl); ok {
		out.Cron = fmt.Sprintf("%d %d * * *", minute, hour)
	} else if daily && morning {
		out.Cron = "0 9 * * *"
	} else if daily {
		out.Cron = "0 9 * * *"
	}

	// --- channel ---
	switch {
	case containsAny(lc, dingtalkMarks):
		out.Channel = "dingtalk"
	case containsAny(lc, feishuMarks):
		out.Channel = "feishu"
	}

	// --- source ---
	if containsAny(lc, aiNewsMarks) || (containsAny(lc, newsMarks) && strings.Contains(lc, "ai")) {
		out.UsesRSS = true
		// Hardcoded canonical RSS hint — production replaces this via LLM.
		out.SourceHint = "https://news.ycombinator.com/rss"
	} else if containsAny(lc, newsMarks) {
		out.UsesRSS = true
		out.SourceHint = "https://news.ycombinator.com/rss"
	}

	return out
}

// extractClock parses "9 点" / "9:00" / "9am" / "上午 9 点" out of nl. It
// only returns ok=true when an unambiguous hour is found near a clock
// marker (avoids matching things like "9 月").
func extractClock(nl string) (hour, minute int, ok bool) {
	lc := strings.ToLower(nl)
	loc := reHour.FindStringIndex(lc)
	if loc == nil {
		return 0, 0, false
	}
	m := reHour.FindStringSubmatch(lc)
	if m == nil {
		return 0, 0, false
	}
	idxH := reHour.SubexpIndex("h")
	idxM := reHour.SubexpIndex("m")
	if idxH < 0 || idxH >= len(m) {
		return 0, 0, false
	}
	var h, mn int
	if _, err := fmt.Sscanf(m[idxH], "%d", &h); err != nil {
		return 0, 0, false
	}
	if idxM >= 0 && idxM < len(m) && m[idxM] != "" {
		_, _ = fmt.Sscanf(m[idxM], "%d", &mn)
	}
	if h < 0 || h > 23 || mn < 0 || mn > 59 {
		return 0, 0, false
	}
	matched := m[0]
	hasClockMarker := strings.ContainsAny(matched, ":点") || strings.Contains(strings.ToLower(matched), "am")
	if !hasClockMarker {
		// Require clock context around the match (e.g. "早上 9").
		head := nl[:loc[0]]
		tail := nl[loc[1]:]
		if !looksLikeClockContext(head + " " + tail) {
			return 0, 0, false
		}
	}
	return h, mn, true
}

func looksLikeClockContext(s string) bool {
	lc := strings.ToLower(s)
	for _, kw := range []string{"早上", "上午", "morning", "点", "am", "时", ":"} {
		if strings.Contains(lc, kw) {
			return true
		}
	}
	return false
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func normalizeLocale(loc string) string {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return "zh-CN"
	}
	return loc
}

// --- manifest assembly ------------------------------------------------------

// slugify turns a free-form Chinese/English input into a SoyaPack name slug.
// The slug must match soyapack.reName: ^[a-z][a-z0-9-]{0,46}[a-z0-9]$.
func slugify(nl string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(nl) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			// Anything non-ASCII (e.g. Chinese) or punctuation becomes a separator.
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "newsbeam-agent"
	}
	if len(s) > 48 {
		s = s[:48]
		s = strings.TrimRight(s, "-")
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "a-" + s
		if len(s) > 48 {
			s = s[:48]
			s = strings.TrimRight(s, "-")
		}
	}
	return s
}

// assembleManifest stitches a minimum-valid Agent manifest from the
// extracted slots. The output is the deterministic, LLM-free baseline.
func assembleManifest(nl string, s slots) *soyapack.Manifest {
	name := slugify(nl)
	if name == "" {
		name = "newsbeam-agent"
	}

	desc := s.Intent
	if len(desc) > 280 {
		desc = desc[:277] + "..."
	}

	m := &soyapack.Manifest{
		SpecVersion: soyapack.SpecVersionV0,
		Kind:        soyapack.KindAgent,
		Name:        name,
		Version:     "0.1.0",
		Description: desc,
		Authors:     []soyapack.Author{{Name: "factory"}},
		License:     "MIT",
		Runtime:     soyapack.RuntimeCompat{Compat: ">=0.1.0 <0.2.0"},
		Determinism: soyapack.DeterminismStateful,
		Entry:       "agent.main",
		Expose: &soyapack.Expose{
			OpenAICompat:   "chat",
			VirtualModelID: "soya:" + name,
		},
	}

	if s.UsesRSS {
		m.Prompt = &soyapack.Prompt{
			Tools: []string{"tool.rss_fetch"},
		}
		m.Uses = append(m.Uses, "tool.rss_fetch")
	}

	if s.Cron != "" {
		schedule := soyapack.ScheduleDecl{
			Cron:       s.Cron,
			TZ:         "Asia/Shanghai",
			MissedFire: "skip",
		}
		if s.SourceHint != "" {
			schedule.Payload = map[string]any{"source": s.SourceHint}
		}
		m.Schedules = append(m.Schedules, schedule)
	}

	if s.Channel != "" {
		m.Channels = append(m.Channels, soyapack.ChannelDecl{
			Kind:            s.Channel,
			BindingTemplate: s.Channel + ".group",
		})
	}

	// Daily-push Agents always declare a long_image artifact — DD-009
	// makes the long image the default broadcast surface for IM channels.
	if len(m.Channels) > 0 {
		m.Artifacts = append(m.Artifacts, soyapack.ArtifactDecl{
			Kind:   "long_image",
			Schema: "newsbeam.v1",
		})
	}

	return m
}

// --- LLM refinement ---------------------------------------------------------

// refineWithLLM asks the configured LLM to produce a richer manifest.
// On any error (network, parse, validation) we discard the LLM output
// and let the caller use the heuristic result. The LLM is never the
// source of truth — it's an upgrade, not a dependency.
func (f *Factory) refineWithLLM(ctx context.Context, nl, locale string, seed *soyapack.Manifest) (*soyapack.Manifest, error) {
	const system = `You are SoyaOS Agent Factory. Convert the user's natural-language
intent into a SoyaPack v0 YAML manifest. Always set spec_version: soyapack.v0
and kind: Agent. Reply with YAML only, no prose, no code fences.`

	seedYAML, err := yaml.Marshal(seed)
	if err != nil {
		return nil, err
	}

	user := fmt.Sprintf("Locale: %s\nIntent: %s\n\nSeed manifest:\n%s",
		locale, nl, string(seedYAML))

	raw, err := f.LLM.Generate(ctx, system, user)
	if err != nil {
		return nil, err
	}
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```yaml")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var out soyapack.Manifest
	if err := yaml.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if err := soyapack.Validate(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

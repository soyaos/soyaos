// Package rssfetch implements the "tool.rss_fetch" Kernel built-in Tool.
//
// NewsBeam (DD-009) depends on a deterministic, side-effect-isolated way
// to pull "what happened in the last day" from RSS feeds. The runtime
// Gate hands this tool a single egress host + URL pair; the tool reads
// the feed, applies a 24-hour window, de-duplicates by link hash and
// sorts results so the most authoritative source surfaces first.
//
// Alpha keeps the parser stdlib-only (encoding/xml) on purpose:
//
//   - No new external dependency to ship in the Kernel binary.
//   - Output stays trivially mock-able from tests (httptest + canned XML).
//   - The minimum RSS 2.0 subset (channel.item.{title,link,pubDate})
//     covers every tech news feed the NewsBeam demo cares about.
//
// Authoritativeness ordering is a hard-coded host whitelist for alpha —
// the same way ContentRoute pinned its initial provider list before
// being switched to a learned router.
package rssfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/soyaos/soyaos/pkg/tooling"
)

// ToolName is the canonical registry name, also used by capability
// declarations in manifests.
const ToolName = "tool.rss_fetch"

// defaultWindow is the look-back applied when Input.Since is the zero value.
const defaultWindow = 24 * time.Hour

// authorityRank assigns a lower score to higher-trust sources (a.k.a.
// "publish first"). Hosts not listed sort last in alphabetical order.
//
// The list is deliberately short for alpha; learned ranking is on the
// roadmap with the Memory + signals work (DD-016).
var authorityRank = map[string]int{
	"bloomberg.com":   0,
	"theverge.com":    1,
	"arstechnica.com": 2,
	"techcrunch.com":  3,
	"x.com":           4,
}

// Tool is the rss_fetch tool handle. Both Client and Now are injection
// points so unit tests can use httptest and a fixed clock.
type Tool struct {
	Client *http.Client
	Now    func() time.Time
}

// Input is the structured argument to Invoke.
type Input struct {
	URL      string
	MaxItems int
	Since    time.Time
}

// Item is one normalized feed entry.
type Item struct {
	Title     string
	Link      string
	Source    string // feed host (e.g. "bloomberg.com")
	Published time.Time
	Hash      string // sha256(link)[:16] in hex, the de-dup key
}

// Name implements an informal name() contract used by the tooling layer.
func (t *Tool) Name() string { return ToolName }

// Invoke fetches in.URL, parses items, applies the 24-hour window,
// de-duplicates by link hash and sorts authoritatively.
func (t *Tool) Invoke(ctx context.Context, in Input) ([]Item, error) {
	if in.URL == "" {
		return nil, errors.New("rssfetch: empty URL")
	}
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	now := time.Now
	if t.Now != nil {
		now = t.Now
	}

	since := in.Since
	if since.IsZero() {
		since = now().Add(-defaultWindow)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("rssfetch: build request: %w", err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml;q=0.9, */*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rssfetch: GET %s: %w", in.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rssfetch: GET %s: status %d", in.URL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return nil, fmt.Errorf("rssfetch: read body: %w", err)
	}

	parsed, err := parseRSS(body)
	if err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(parsed))
	seen := map[string]struct{}{}
	for _, p := range parsed {
		if p.Link == "" {
			continue
		}
		pub := parseDate(p.PubDate)
		if !pub.IsZero() && pub.Before(since) {
			continue
		}
		hash := hashLink(p.Link)
		if _, dup := seen[hash]; dup {
			continue
		}
		seen[hash] = struct{}{}
		items = append(items, Item{
			Title:     strings.TrimSpace(p.Title),
			Link:      p.Link,
			Source:    hostOf(p.Link),
			Published: pub,
			Hash:      hash,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		ri, oki := authorityRank[items[i].Source]
		rj, okj := authorityRank[items[j].Source]
		switch {
		case oki && okj && ri != rj:
			return ri < rj
		case oki && !okj:
			return true
		case !oki && okj:
			return false
		}
		// Same authority bucket → newer first.
		if !items[i].Published.Equal(items[j].Published) {
			return items[i].Published.After(items[j].Published)
		}
		return items[i].Source < items[j].Source
	})

	if in.MaxItems > 0 && len(items) > in.MaxItems {
		items = items[:in.MaxItems]
	}
	return items, nil
}

// --- RSS parsing -----------------------------------------------------------

type rssEnvelope struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
}

func parseRSS(body []byte) ([]rssItem, error) {
	var env rssEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("rssfetch: parse XML: %w", err)
	}
	return env.Channel.Items, nil
}

// pubDate parsing — try a few canonical layouts before giving up. Real
// feeds drift across RFC 822, RFC 1123 and a handful of homegrown
// variants; we accept the lot.
var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05Z",
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func hashLink(link string) string {
	sum := sha256.Sum256([]byte(link))
	return hex.EncodeToString(sum[:])[:16]
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Host)
	return strings.TrimPrefix(h, "www.")
}

// --- registry integration --------------------------------------------------

// Builtin returns the tooling.Tool descriptor used by the kernel registry.
func Builtin() tooling.Tool {
	t := &Tool{}
	return tooling.Tool{
		Name:        ToolName,
		Description: "Fetch an RSS feed, apply a 24h window, de-dupe by link hash, and rank by source authority.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":       map[string]any{"type": "string"},
				"max_items": map[string]any{"type": "integer"},
				"since":     map[string]any{"type": "string", "format": "date-time"},
			},
			"required": []any{"url"},
		},
		OutputType: "application/json",
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in := Input{}
			if v, ok := input["url"].(string); ok {
				in.URL = v
			}
			if v, ok := input["max_items"].(int); ok {
				in.MaxItems = v
			} else if v, ok := input["max_items"].(float64); ok {
				in.MaxItems = int(v)
			}
			if v, ok := input["since"].(string); ok && v != "" {
				if ts, err := time.Parse(time.RFC3339, v); err == nil {
					in.Since = ts
				}
			}
			return t.Invoke(ctx, in)
		},
	}
}

// rssfetch_test.go uses httptest to feed canned RSS 2.0 payloads through
// the tool. This pins the 24-hour window, link de-duplication and the
// authoritative-host ordering against accidental regressions.
package rssfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test</title>
    <item>
      <title>Bloomberg story</title>
      <link>https://bloomberg.com/news/articles/abc</link>
      <pubDate>%s</pubDate>
    </item>
    <item>
      <title>Verge story</title>
      <link>https://theverge.com/2026/05/19/x</link>
      <pubDate>%s</pubDate>
    </item>
    <item>
      <title>Duplicate Verge link</title>
      <link>https://theverge.com/2026/05/19/x</link>
      <pubDate>%s</pubDate>
    </item>
    <item>
      <title>Old story (outside 24h)</title>
      <link>https://arstechnica.com/oldie</link>
      <pubDate>%s</pubDate>
    </item>
    <item>
      <title>Some random blog</title>
      <link>https://example.org/post-1</link>
      <pubDate>%s</pubDate>
    </item>
  </channel>
</rss>`

func TestRSSFetch_Window_DedupAndAuthority(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-1 * time.Hour).Format(time.RFC1123Z)
	old := now.Add(-48 * time.Hour).Format(time.RFC1123Z)

	body := fmt.Sprintf(sampleFeed, recent, recent, recent, old, recent)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	tl := &Tool{Now: func() time.Time { return now }}
	items, err := tl.Invoke(context.Background(), Input{URL: srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items (window+dedup), got %d: %+v", len(items), items)
	}
	// Bloomberg should beat Verge in the ranking.
	if items[0].Source != "bloomberg.com" || items[1].Source != "theverge.com" {
		t.Errorf("authority ordering wrong: %+v", items)
	}
	// example.org has no authority rank → sorts last.
	if items[2].Source != "example.org" {
		t.Errorf("unknown host should sort last, got %+v", items)
	}
	// Hash must be deterministic + 16 hex chars.
	for _, it := range items {
		if len(it.Hash) != 16 {
			t.Errorf("Hash %q len = %d, want 16", it.Hash, len(it.Hash))
		}
	}
}

func TestRSSFetch_MaxItems_Truncates(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-1 * time.Hour).Format(time.RFC1123Z)
	body := fmt.Sprintf(sampleFeed, recent, recent, recent, recent, recent)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	tl := &Tool{Now: func() time.Time { return now }}
	items, err := tl.Invoke(context.Background(), Input{URL: srv.URL, MaxItems: 2})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("MaxItems=2 should truncate, got %d", len(items))
	}
}

func TestRSSFetch_HTTPError_Bubbles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tl := &Tool{}
	if _, err := tl.Invoke(context.Background(), Input{URL: srv.URL}); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
}

func TestRSSFetch_EmptyURL_Errors(t *testing.T) {
	tl := &Tool{}
	if _, err := tl.Invoke(context.Background(), Input{}); err == nil {
		t.Fatal("want error on empty URL")
	}
}

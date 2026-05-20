// degradation_test.go pins the DD-006 degradation rules: rich → image → text.
// The tests wire fake LongImageProvider / ShortlinkProvider so the
// degradation logic itself stays under test without dragging in the
// chromedp renderer or the bbolt store.
package connectors_test

import (
	"context"
	"strings"
	"testing"

	"github.com/soyaos/soyaos/pkg/connectors"
)

func staticLongImage(url string) connectors.LongImageProvider {
	return func(_ context.Context, _ connectors.Message) (string, error) { return url, nil }
}

func staticShortener(short string) connectors.ShortlinkProvider {
	return func(_ context.Context, _ string) (string, error) { return short, nil }
}

func TestDegrade_RichTarget_PassThrough(t *testing.T) {
	in := connectors.Message{
		Text:        "hello",
		Attachments: []connectors.Attachment{{Kind: "card", URL: "https://x/1"}},
	}
	out, err := connectors.Degrade(context.Background(), in,
		connectors.ChannelCapabilities{SupportsRichCard: true, SupportsMarkdown: true, SupportsImage: true},
		staticLongImage(""), nil)
	if err != nil {
		t.Fatalf("Degrade: %v", err)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].Kind != "card" {
		t.Errorf("rich pass-through dropped attachments: %+v", out)
	}
}

func TestDegrade_NoRichButHasImage_UsesLongImage(t *testing.T) {
	in := connectors.Message{
		Text:        "Daily Digest",
		Attachments: []connectors.Attachment{{Kind: "card", URL: "https://x/1"}, {Kind: "file", URL: "https://x/2"}},
	}
	out, err := connectors.Degrade(context.Background(), in,
		connectors.ChannelCapabilities{SupportsImage: true, MaxTextLength: 80},
		staticLongImage("https://oss/long-image.png"), nil)
	if err != nil {
		t.Fatalf("Degrade: %v", err)
	}
	if len(out.Attachments) != 1 {
		t.Fatalf("want 1 attachment after collapse, got %d", len(out.Attachments))
	}
	if out.Attachments[0].Kind != "image" || out.Attachments[0].URL != "https://oss/long-image.png" {
		t.Errorf("collapsed attachment = %+v", out.Attachments[0])
	}
}

func TestDegrade_NoneSupportedButHasText_TruncatesAndAppendsShortLink(t *testing.T) {
	in := connectors.Message{
		Text: "这是一条很长的资讯标题，应该被截短到合适的长度并附上短链",
		Attachments: []connectors.Attachment{
			{Kind: "card", URL: "https://example.com/full-article-url"},
		},
	}
	out, err := connectors.Degrade(context.Background(), in,
		connectors.ChannelCapabilities{MaxTextLength: 16},
		nil, staticShortener("https://s/AbCdEfGh"))
	if err != nil {
		t.Fatalf("Degrade: %v", err)
	}
	if !strings.Contains(out.Text, "https://s/AbCdEfGh") {
		t.Errorf("text missing short link: %q", out.Text)
	}
	// Rune-length cap: shorter than ~MaxTextLength + short-link length.
	if got := len([]rune(out.Text)); got > 16+len("https://s/AbCdEfGh") {
		t.Errorf("truncate overshoot: %d runes (%q)", got, out.Text)
	}
	if len(out.Attachments) != 0 {
		t.Errorf("text-only path should drop attachments, got %+v", out.Attachments)
	}
}

func TestDegrade_TextOnly_NoAttachment_NoShortlink(t *testing.T) {
	in := connectors.Message{Text: "short"}
	out, err := connectors.Degrade(context.Background(), in,
		connectors.ChannelCapabilities{MaxTextLength: 100},
		nil, staticShortener("https://s/x"))
	if err != nil {
		t.Fatalf("Degrade: %v", err)
	}
	if strings.Contains(out.Text, "https://s/") {
		t.Errorf("no attachment → no shortlink expected, got %q", out.Text)
	}
}

func TestDegrade_AllImageAttachments_StayUntouched(t *testing.T) {
	// If everything is already an image, the image-capable channel can
	// take them as-is — no need to collapse via long-image renderer.
	in := connectors.Message{
		Attachments: []connectors.Attachment{{Kind: "image", URL: "https://x/img.png"}},
	}
	out, err := connectors.Degrade(context.Background(), in,
		connectors.ChannelCapabilities{SupportsImage: true},
		func(_ context.Context, _ connectors.Message) (string, error) {
			t.Fatal("LongImageProvider should not be called when attachments are already images")
			return "", nil
		}, nil)
	if err != nil {
		t.Fatalf("Degrade: %v", err)
	}
	if len(out.Attachments) != 1 || out.Attachments[0].URL != "https://x/img.png" {
		t.Errorf("image attachments should pass through, got %+v", out.Attachments)
	}
}

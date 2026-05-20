package connectors

import (
	"context"
	"fmt"
	"strings"
)

// ChannelCapabilities describes the rich-content surface a given channel
// supports. DD-006's "richlinks must degrade" rule is enforced by
// Degrade() — callers query the capabilities of the target channel
// (DingTalk supports rich markdown; WeChat Public Account broadcast does
// not), and Degrade rewrites the Message in place.
//
// Capabilities are intentionally a struct of bools rather than an enum:
// future Channels will fill in their own slice of the matrix, and a
// struct keeps degradation logic explicit per-feature (instead of a
// blanket "minimal" level that hides which dimension fell short).
type ChannelCapabilities struct {
	SupportsRichCard bool // feedCard / actionCard / news card
	SupportsMarkdown bool
	SupportsImage    bool // direct image attach / URL embed
	MaxTextLength    int  // 0 → unbounded
}

// LongImageProvider produces a long-image artifact URL on demand. The
// degradation path uses this when the channel can carry an image but
// not a rich card: the original attachments are collapsed into one
// long-image URL that the channel renders as a single picture.
//
// Implementations typically call the artifact registry's long_image
// renderer and stash the result in OSS, returning the public URL.
type LongImageProvider func(ctx context.Context, m Message) (string, error)

// ShortlinkProvider shortens a long URL to a token suitable for appending
// to a text-only fallback message. Use pkg/shortlink as the canonical
// implementation; the function-typed seam keeps tests trivial.
type ShortlinkProvider func(ctx context.Context, longURL string) (string, error)

// Degrade rewrites m so it fits target's capabilities. The rules
// (DD-006 §"Richlinks degradation"):
//
//  1. If the channel can render rich cards (or markdown), pass through.
//  2. If not, but it supports images: replace attachments with a single
//     long-image attachment via lip. The text payload becomes the
//     message title only.
//  3. If neither rich nor image is supported: truncate text to
//     MaxTextLength and append a short link via sp (pointing at the
//     original artifact URL). This is the WeChat-public-account
//     passive-reply path.
//
// Degrade never returns an error from the capability inspection itself —
// any provider failures (long-image rendering, shortlink minting) cause
// the function to return the *partially* degraded message + the error,
// so callers can fall back further if they like.
func Degrade(ctx context.Context, m Message, target ChannelCapabilities, lip LongImageProvider, sp ShortlinkProvider) (Message, error) {
	// Path 1 — rich-card / markdown channels keep the original.
	if target.SupportsRichCard || target.SupportsMarkdown {
		return m, nil
	}

	// Path 2 — image-capable but not rich. Collapse non-image
	// attachments into one long_image; if the message is already pure
	// images, pass through (don't waste a render). Either way, the
	// channel is image-capable so we return here.
	if target.SupportsImage {
		if hasNonImageAttachment(m) && lip != nil {
			url, err := lip(ctx, m)
			if err != nil {
				return m, fmt.Errorf("connectors: degrade long-image: %w", err)
			}
			m.Attachments = []Attachment{{
				Kind: "image",
				MIME: "image/png",
				URL:  url,
			}}
		}
		// Truncate text — image broadcasts usually want short captions.
		if target.MaxTextLength > 0 {
			m.Text = truncate(m.Text, target.MaxTextLength)
		}
		return m, nil
	}

	// Path 3 — pure text. Append a short link if we have an attachment
	// the caller will want recipients to be able to reach.
	if target.MaxTextLength > 0 {
		m.Text = truncate(m.Text, target.MaxTextLength)
	}
	if sp != nil {
		if rich := firstAttachmentURL(m); rich != "" {
			short, err := sp(ctx, rich)
			if err != nil {
				return m, fmt.Errorf("connectors: degrade shortlink: %w", err)
			}
			// Re-truncate to leave room for the appended short link.
			// All counts are in runes — MaxTextLength is the *character*
			// budget, mirroring the truncate() contract.
			suffix := "\n" + short
			if target.MaxTextLength > 0 {
				room := target.MaxTextLength - runeLen(suffix)
				if room < 0 {
					room = 0
				}
				m.Text = truncate(m.Text, room)
			}
			m.Text = strings.TrimRight(m.Text, "\n") + suffix
		}
	}
	m.Attachments = nil
	return m, nil
}

// hasNonImageAttachment returns true when at least one attachment is
// not already a plain image — these are the payloads that need to be
// folded into the long-image fallback.
func hasNonImageAttachment(m Message) bool {
	for _, a := range m.Attachments {
		if a.Kind != "image" {
			return true
		}
	}
	return false
}

// firstAttachmentURL returns the first attachment URL, if any. Used to
// pick the "main link" worth shortening for text-only channels.
func firstAttachmentURL(m Message) string {
	for _, a := range m.Attachments {
		if a.URL != "" {
			return a.URL
		}
	}
	return ""
}

// truncate cuts s to at most n runes (not bytes — Chinese channels count
// characters, not encoded bytes). n<=0 returns "".
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// runeLen returns the number of runes in s. (Cheap helper used by the
// degradation path to keep all budget arithmetic in runes.)
func runeLen(s string) int {
	return len([]rune(s))
}

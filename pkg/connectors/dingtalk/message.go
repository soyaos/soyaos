// Package dingtalk implements the DingTalk Channel Connector (DD-006).
//
// DingTalk is the first "real" Channel — picked first because (a) it has
// the cleanest webhook contract of any 中国 IM platform we care about,
// and (b) NewsBeam's morning-digest demo lives in DingTalk group chats.
//
// The connector is split into three files:
//
//   - message.go  — the canonical Message envelope shared by both halves.
//   - outbound.go — SoyaOS Message → POST /robot/send (with OSS-uploaded
//     image URL for image kinds).
//   - inbound.go  — POST /webhook/dingtalk/{binding_id} → canonical
//     Message, with @-bot keyword + HMAC-SHA256 signature verification
//     and a 5-minute timestamp replay window.
//
// Image payloads do not carry pixels. DingTalk's robot/send endpoint
// only accepts URLs for images, so callers must upload the long_image
// artifact to OSS first and pass the resulting URL here. This keeps the
// connector free of upload concerns and lets the platform.MediaService
// (project_avatar_media_service note) own the upload contract.
package dingtalk

// MessageKind enumerates the DingTalk message shapes the connector
// understands. The Outbound serializer picks the JSON envelope based on
// this field; the Inbound parser maps DingTalk's msgtype back into it.
type MessageKind string

const (
	KindText       MessageKind = "text"
	KindMarkdown   MessageKind = "markdown"
	KindFeedCard   MessageKind = "feedCard"
	KindActionCard MessageKind = "actionCard"
	KindImage      MessageKind = "image"
)

// Message is the canonical DingTalk envelope. Fields are populated per
// Kind:
//
//   - text          → Text
//   - markdown      → Title + Markdown
//   - image         → ImageURL (must be OSS-uploaded, not raw bytes)
//   - feedCard      → FeedLinks
//   - actionCard    → Title + Markdown + ActionButtons
//
// At is the optional @-mention block; UserID is the inbound user's
// senderStaffId (or unionId, depending on the bot's permissions).
type Message struct {
	Kind          MessageKind
	BindingID     string
	UserID        string
	Title         string
	Text          string
	Markdown      string
	ImageURL      string
	FeedLinks     []FeedLink
	ActionButtons []ActionButton
	At            *AtBlock
}

// FeedLink is one entry inside a feedCard body.
type FeedLink struct {
	Title      string
	MessageURL string
	PicURL     string
}

// ActionButton is one row inside an actionCard.
type ActionButton struct {
	Title     string
	ActionURL string
}

// AtBlock is the @-mention block on inbound/outbound messages.
type AtBlock struct {
	AtMobiles []string
	AtUserIds []string
	IsAtAll   bool
}

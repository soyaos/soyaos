package dingtalk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ReplayWindow is how old an inbound timestamp may be before the
// connector rejects it as a replay. DingTalk's documented limit is 1 hour,
// but tightening to 5 minutes both meets the spec and minimizes blast
// radius if a Secret leaks.
const ReplayWindow = 5 * time.Minute

// Inbound handles DingTalk webhook callbacks. One Inbound instance can
// serve multiple bindings — Handler(bindingID, ...) returns an
// http.Handler scoped to that binding's id, so the kernel can mount
// per-binding routes under /webhook/dingtalk/{binding_id}.
//
// Verification is the official DingTalk recipe:
//
//   - The request carries timestamp + sign query params.
//   - sign = base64(HMAC-SHA256(secret, timestamp+"\n"+secret)).
//   - timestamp must be within ReplayWindow of `now`.
//
// On verification failure we return 401 (so DingTalk's retry queue logs
// the rejection). On successful parse we 200 with `{"ok":true}` and hand
// the canonical Message to sink.
type Inbound struct {
	Secret string
	// Now is the time source used during signature verification. Tests
	// inject a fixed clock; production leaves it nil and falls back to
	// time.Now.
	Now func() time.Time
}

// Handler returns the http.Handler for one binding. sink is called once
// per accepted message — the handler does not block waiting for the
// kernel to consume it; sink is expected to be non-blocking (channel
// send into a buffered queue is typical).
func (i *Inbound) Handler(bindingID string, sink func(Message)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		ts := r.URL.Query().Get("timestamp")
		sign := r.URL.Query().Get("sign")
		if err := i.verify(ts, sign); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		msg, err := parseInbound(body, bindingID)
		if err != nil {
			http.Error(w, "parse body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if sink != nil {
			sink(msg)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// verify implements DingTalk's webhook signature scheme.
func (i *Inbound) verify(timestamp, sign string) error {
	if i.Secret == "" {
		return errors.New("dingtalk: webhook secret not configured")
	}
	if timestamp == "" || sign == "" {
		return errors.New("dingtalk: missing timestamp / sign")
	}
	tsMillis, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("dingtalk: bad timestamp")
	}
	now := time.Now
	if i.Now != nil {
		now = i.Now
	}
	delta := now().Sub(time.UnixMilli(tsMillis))
	if delta < 0 {
		delta = -delta
	}
	if delta > ReplayWindow {
		return errors.New("dingtalk: timestamp outside replay window")
	}
	expected := signInbound(timestamp, i.Secret)
	if !hmac.Equal([]byte(expected), []byte(sign)) {
		return errors.New("dingtalk: signature mismatch")
	}
	return nil
}

// signInbound mirrors signOutbound — same scheme, exposed separately so
// the inbound caller doesn't import outbound's identifier names.
func signInbound(timestamp, secret string) string {
	msg := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// --- payload parsing -------------------------------------------------------

// dingPayload mirrors the documented DingTalk webhook envelope. We
// decode only the fields we care about — DingTalk adds new ones over
// time and the parser must remain forward-compatible.
type dingPayload struct {
	MsgType         string            `json:"msgtype"`
	Text            *dingText         `json:"text,omitempty"`
	Markdown        *dingMarkdown     `json:"markdown,omitempty"`
	SenderStaffID   string            `json:"senderStaffId,omitempty"`
	SenderID        string            `json:"senderId,omitempty"`
	SenderNick      string            `json:"senderNick,omitempty"`
	AtUsers         []dingAtUser      `json:"atUsers,omitempty"`
	IsInAtList      bool              `json:"isInAtList,omitempty"`
	ConversationID  string            `json:"conversationId,omitempty"`
	MsgID           string            `json:"msgId,omitempty"`
	CreateAt        int64             `json:"createAt,omitempty"`
	Extras          map[string]string `json:"extras,omitempty"`
}

type dingText struct {
	Content string `json:"content"`
}

type dingMarkdown struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type dingAtUser struct {
	DingtalkID string `json:"dingtalkId"`
	StaffID    string `json:"staffId"`
}

func parseInbound(body []byte, bindingID string) (Message, error) {
	var p dingPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Message{}, err
	}
	m := Message{BindingID: bindingID, UserID: p.SenderStaffID}
	if m.UserID == "" {
		m.UserID = p.SenderID
	}
	switch strings.ToLower(p.MsgType) {
	case "text":
		m.Kind = KindText
		if p.Text != nil {
			m.Text = strings.TrimSpace(p.Text.Content)
		}
	case "markdown":
		m.Kind = KindMarkdown
		if p.Markdown != nil {
			m.Title = p.Markdown.Title
			m.Markdown = p.Markdown.Text
		}
	default:
		// Unknown msgtype → fall back to text with raw body so callers
		// can still log/inspect, instead of dropping the event.
		m.Kind = KindText
		m.Text = string(body)
	}
	if len(p.AtUsers) > 0 {
		at := &AtBlock{}
		for _, u := range p.AtUsers {
			if u.StaffID != "" {
				at.AtUserIds = append(at.AtUserIds, u.StaffID)
			} else if u.DingtalkID != "" {
				at.AtUserIds = append(at.AtUserIds, u.DingtalkID)
			}
		}
		m.At = at
	}
	return m, nil
}

package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultRobotEndpoint is DingTalk's canonical webhook URL.
const DefaultRobotEndpoint = "https://oapi.dingtalk.com/robot/send"

// Outbound posts Messages to a DingTalk custom robot.
//
// Robots are addressed by AccessToken (queried via ?access_token=...).
// If Secret is non-empty the connector adds the official HMAC signature
// query parameters (timestamp + sign) that locked-down robots require.
// Both fields are bound at connector construction time so the routing
// surface stays "give me a Message" rather than "give me a Message plus
// these creds".
type Outbound struct {
	Client      *http.Client
	Endpoint    string // override (test only); defaults to DefaultRobotEndpoint
	AccessToken string
	Secret      string // optional; enables HMAC signing
}

// Send serializes m for DingTalk and POSTs it to the robot endpoint.
func (o *Outbound) Send(ctx context.Context, m Message) error {
	if o.AccessToken == "" {
		return errors.New("dingtalk: empty AccessToken")
	}
	payload, err := buildPayload(m)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dingtalk: marshal payload: %w", err)
	}

	endpoint := o.Endpoint
	if endpoint == "" {
		endpoint = DefaultRobotEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("dingtalk: parse endpoint: %w", err)
	}
	q := u.Query()
	q.Set("access_token", o.AccessToken)
	if o.Secret != "" {
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		sign := signOutbound(ts, o.Secret)
		q.Set("timestamp", ts)
		q.Set("sign", sign)
	}
	u.RawQuery = q.Encode()

	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("dingtalk: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("dingtalk: send failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	// DingTalk returns {"errcode":0,"errmsg":"ok"} on success and non-zero
	// errcode on failure even with HTTP 200.
	var envelope struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &envelope); err == nil && envelope.ErrCode != 0 {
		return fmt.Errorf("dingtalk: errcode=%d errmsg=%s", envelope.ErrCode, envelope.ErrMsg)
	}
	return nil
}

// signOutbound implements DingTalk's robot signature scheme:
// HMAC-SHA256(secret, timestamp+"\n"+secret) → base64 → URL-encoded sign.
//
// (The Inbound webhook uses a different scheme — see inbound.go.)
func signOutbound(timestamp, secret string) string {
	msg := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// buildPayload converts m into the DingTalk JSON envelope.
func buildPayload(m Message) (map[string]any, error) {
	out := map[string]any{}
	if m.At != nil {
		atBlock := map[string]any{}
		if len(m.At.AtMobiles) > 0 {
			atBlock["atMobiles"] = m.At.AtMobiles
		}
		if len(m.At.AtUserIds) > 0 {
			atBlock["atUserIds"] = m.At.AtUserIds
		}
		if m.At.IsAtAll {
			atBlock["isAtAll"] = true
		}
		if len(atBlock) > 0 {
			out["at"] = atBlock
		}
	}

	switch m.Kind {
	case KindText:
		if m.Text == "" {
			return nil, errors.New("dingtalk: text message has empty Text")
		}
		out["msgtype"] = "text"
		out["text"] = map[string]any{"content": m.Text}
	case KindMarkdown:
		if m.Markdown == "" || m.Title == "" {
			return nil, errors.New("dingtalk: markdown requires Title + Markdown")
		}
		out["msgtype"] = "markdown"
		out["markdown"] = map[string]any{"title": m.Title, "text": m.Markdown}
	case KindImage:
		if m.ImageURL == "" {
			return nil, errors.New("dingtalk: image kind needs ImageURL (OSS-uploaded)")
		}
		// DingTalk expects images embedded inside a markdown payload —
		// the dedicated "image" msgtype only exists in newer SDKs and is
		// not yet universally supported. Use the markdown form so we
		// reach every robot version.
		title := m.Title
		if title == "" {
			title = "image"
		}
		out["msgtype"] = "markdown"
		out["markdown"] = map[string]any{
			"title": title,
			"text":  fmt.Sprintf("![](%s)", m.ImageURL),
		}
	case KindFeedCard:
		if len(m.FeedLinks) == 0 {
			return nil, errors.New("dingtalk: feedCard requires at least one FeedLink")
		}
		links := make([]map[string]any, 0, len(m.FeedLinks))
		for _, l := range m.FeedLinks {
			links = append(links, map[string]any{
				"title":      l.Title,
				"messageURL": l.MessageURL,
				"picURL":     l.PicURL,
			})
		}
		out["msgtype"] = "feedCard"
		out["feedCard"] = map[string]any{"links": links}
	case KindActionCard:
		if m.Markdown == "" || m.Title == "" {
			return nil, errors.New("dingtalk: actionCard requires Title + Markdown")
		}
		card := map[string]any{
			"title": m.Title,
			"text":  m.Markdown,
		}
		if len(m.ActionButtons) > 0 {
			btns := make([]map[string]any, 0, len(m.ActionButtons))
			for _, b := range m.ActionButtons {
				btns = append(btns, map[string]any{"title": b.Title, "actionURL": b.ActionURL})
			}
			card["btns"] = btns
		}
		out["msgtype"] = "actionCard"
		out["actionCard"] = card
	default:
		return nil, fmt.Errorf("dingtalk: unknown message kind %q", m.Kind)
	}
	return out, nil
}

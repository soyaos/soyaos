// outbound_test.go uses httptest to assert the wire-level shape of every
// supported MessageKind plus the DingTalk-specific access_token /
// signature query parameters. The tests never hit the real DingTalk —
// they're a contract pin, not an integration probe.
package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOutbound_RequiresAccessToken(t *testing.T) {
	o := &Outbound{}
	if err := o.Send(context.Background(), Message{Kind: KindText, Text: "hi"}); err == nil {
		t.Fatal("expected error on empty AccessToken")
	}
}

func TestOutbound_Text_PayloadShape(t *testing.T) {
	var captured map[string]any
	var capturedToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = r.URL.Query().Get("access_token")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk-123"}
	if err := o.Send(context.Background(), Message{Kind: KindText, Text: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if capturedToken != "tk-123" {
		t.Errorf("access_token = %q, want tk-123", capturedToken)
	}
	if captured["msgtype"] != "text" {
		t.Errorf("msgtype = %v, want text", captured["msgtype"])
	}
	text, ok := captured["text"].(map[string]any)
	if !ok || text["content"] != "hello" {
		t.Errorf("text.content = %v, want hello", captured["text"])
	}
}

func TestOutbound_Markdown_PayloadShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk"}
	err := o.Send(context.Background(), Message{
		Kind:     KindMarkdown,
		Title:    "Daily Digest",
		Markdown: "## hi\n- one\n- two",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if captured["msgtype"] != "markdown" {
		t.Errorf("msgtype = %v, want markdown", captured["msgtype"])
	}
	md, _ := captured["markdown"].(map[string]any)
	if md["title"] != "Daily Digest" {
		t.Errorf("title = %v", md["title"])
	}
}

func TestOutbound_Image_RequiresURL(t *testing.T) {
	o := &Outbound{AccessToken: "tk"}
	if err := o.Send(context.Background(), Message{Kind: KindImage}); err == nil {
		t.Fatal("expected error: image without ImageURL")
	}
}

func TestOutbound_Image_AsMarkdownPayload(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk"}
	err := o.Send(context.Background(), Message{Kind: KindImage, ImageURL: "https://oss/foo.png", Title: "img"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	md, _ := captured["markdown"].(map[string]any)
	if got, _ := md["text"].(string); !strings.Contains(got, "![](https://oss/foo.png)") {
		t.Errorf("image markdown text = %q", got)
	}
}

func TestOutbound_FeedCard_PayloadShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk"}
	err := o.Send(context.Background(), Message{Kind: KindFeedCard, FeedLinks: []FeedLink{{Title: "T", MessageURL: "https://x/1", PicURL: "https://x/p"}}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if captured["msgtype"] != "feedCard" {
		t.Errorf("msgtype = %v, want feedCard", captured["msgtype"])
	}
}

func TestOutbound_ActionCard_PayloadShape(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk"}
	err := o.Send(context.Background(), Message{
		Kind:          KindActionCard,
		Title:         "T",
		Markdown:      "body",
		ActionButtons: []ActionButton{{Title: "Open", ActionURL: "https://x"}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if captured["msgtype"] != "actionCard" {
		t.Errorf("msgtype = %v, want actionCard", captured["msgtype"])
	}
}

func TestOutbound_SignedRequest_AttachesTimestampAndSign(t *testing.T) {
	var capturedTS, capturedSign string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTS = r.URL.Query().Get("timestamp")
		capturedSign = r.URL.Query().Get("sign")
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer srv.Close()
	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk", Secret: "SECRET"}
	if err := o.Send(context.Background(), Message{Kind: KindText, Text: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if capturedTS == "" || capturedSign == "" {
		t.Errorf("expected signed request to attach timestamp+sign, got ts=%q sign=%q", capturedTS, capturedSign)
	}
}

func TestOutbound_DingTalkErrCode_Propagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":300001,"errmsg":"bad token"}`))
	}))
	defer srv.Close()
	o := &Outbound{Endpoint: srv.URL, AccessToken: "tk"}
	err := o.Send(context.Background(), Message{Kind: KindText, Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "300001") {
		t.Fatalf("expected error to include errcode 300001, got %v", err)
	}
}

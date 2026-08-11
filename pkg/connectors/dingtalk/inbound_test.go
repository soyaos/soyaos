// inbound_test.go pins HMAC verification (happy path + replay rejection)
// and the JSON→canonical-Message translation. No real DingTalk involved;
// httptest stands in for the kernel's webhook router.
package dingtalk

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sign(secret string, ts int64) string {
	tsStr := strconv.FormatInt(ts, 10)
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(tsStr + "\n" + secret))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func TestInbound_HappyPath_DeliversText(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	inb := &Inbound{Secret: "S", Now: func() time.Time { return now }}

	var received Message
	h := inb.Handler("bind-1", func(m Message) { received = m })
	srv := httptest.NewServer(h)
	defer srv.Close()

	ts := now.UnixMilli()
	url := srv.URL + "?timestamp=" + strconv.FormatInt(ts, 10) + "&sign=" + sign("S", ts)
	body := []byte(`{"msgtype":"text","text":{"content":"hi bot"},"senderStaffId":"user-9"}`)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if received.BindingID != "bind-1" {
		t.Errorf("BindingID = %q", received.BindingID)
	}
	if received.Kind != KindText || received.Text != "hi bot" {
		t.Errorf("Message = %+v", received)
	}
	if received.UserID != "user-9" {
		t.Errorf("UserID = %q", received.UserID)
	}
}

func TestInbound_RejectsReplay(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	inb := &Inbound{Secret: "S", Now: func() time.Time { return now }}
	h := inb.Handler("bind-1", func(_ Message) {})
	srv := httptest.NewServer(h)
	defer srv.Close()

	// 10 minutes in the past — outside the 5 min replay window.
	old := now.Add(-10 * time.Minute).UnixMilli()
	url := srv.URL + "?timestamp=" + strconv.FormatInt(old, 10) + "&sign=" + sign("S", old)
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("replay should yield 401, got %d", resp.StatusCode)
	}
}

func TestInbound_BadSignature_Rejected(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	inb := &Inbound{Secret: "S", Now: func() time.Time { return now }}
	srv := httptest.NewServer(inb.Handler("b", func(_ Message) {}))
	defer srv.Close()
	ts := now.UnixMilli()
	url := srv.URL + "?timestamp=" + strconv.FormatInt(ts, 10) + "&sign=" + sign("OTHER", ts)
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestInbound_RejectsGET(t *testing.T) {
	inb := &Inbound{Secret: "S"}
	srv := httptest.NewServer(inb.Handler("b", func(_ Message) {}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestInbound_MissingSignature(t *testing.T) {
	inb := &Inbound{Secret: "S"}
	srv := httptest.NewServer(inb.Handler("b", func(_ Message) {}))
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestInbound_Markdown_Parsing(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	inb := &Inbound{Secret: "S", Now: func() time.Time { return now }}
	var got Message
	srv := httptest.NewServer(inb.Handler("b", func(m Message) { got = m }))
	defer srv.Close()
	ts := now.UnixMilli()
	body := []byte(`{"msgtype":"markdown","markdown":{"title":"T","text":"#H"},"senderStaffId":"u"}`)
	url := srv.URL + "?timestamp=" + strconv.FormatInt(ts, 10) + "&sign=" + sign("S", ts)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if got.Kind != KindMarkdown || got.Markdown != "#H" || got.Title != "T" {
		t.Errorf("parsed = %+v", got)
	}
}

func TestInbound_MissingSecret(t *testing.T) {
	inb := &Inbound{}
	srv := httptest.NewServer(inb.Handler("b", func(_ Message) {}))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"?timestamp=1&sign=x", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	// Body should mention secret config.
	body, _ := readAll(resp)
	if !strings.Contains(string(body), "secret") {
		t.Errorf("error body should mention secret: %q", body)
	}
}

func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}

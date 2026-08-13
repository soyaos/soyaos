package relay

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestServerForwardsOpaquePayloadAndRejectsSideTakeover(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	serverSocket := listenUDP4(t)
	server, err := NewServer(serverSocket, ServerConfig{
		Secret:             secret,
		RateBytesPerSecond: 1024 * 1024,
		BurstBytes:         1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Serve(ctx) }()

	token, err := IssueToken(secret, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	moon := listenUDP4(t)
	comet := listenUDP4(t)
	hijacker := listenUDP4(t)
	serverAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: serverSocket.LocalAddr().(*net.UDPAddr).Port,
	}

	writeFrame(t, moon, serverAddr, encodeFrame(token, SideMoon, nil))
	writeFrame(t, comet, serverAddr, encodeFrame(token, SideComet, nil))
	payload := []byte("opaque-quic-ciphertext")
	writeFrame(t, comet, serverAddr, encodeFrame(token, SideComet, payload))
	got := readFrame(t, moon)
	_, side, forwarded, err := decodeFrame(got)
	if err != nil || side != SideComet || string(forwarded) != string(payload) {
		t.Fatalf("forwarded side=%v payload=%q err=%v", side, forwarded, err)
	}

	// A third address with the bearer token cannot replace the Moon address
	// after the legitimate Moon side has registered.
	writeFrame(t, hijacker, serverAddr, encodeFrame(token, SideMoon, []byte("takeover")))
	_ = comet.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buffer := make([]byte, 1024)
	if _, _, err := comet.ReadFrom(buffer); err == nil {
		t.Fatal("relay forwarded a side-takeover datagram")
	}
	if server.Stats().DroppedFrames == 0 {
		t.Fatal("takeover should increment dropped frame count")
	}
}

func listenUDP4(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func writeFrame(t *testing.T, conn *net.UDPConn, addr net.Addr, frame []byte) {
	t.Helper()
	if _, err := conn.WriteTo(frame, addr); err != nil {
		t.Fatal(err)
	}
}

func readFrame(t *testing.T, conn *net.UDPConn) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 64*1024)
	n, _, err := conn.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), buffer[:n]...)
}

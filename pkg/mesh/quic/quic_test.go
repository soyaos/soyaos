package quic

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"github.com/soyaos/soyaos/pkg/mesh"
	"github.com/soyaos/soyaos/pkg/mesh/relay"
)

func TestTransport_NewKeepsTLSConfig(t *testing.T) {
	cfg := &tls.Config{ServerName: "moon.example"}
	tr := New(cfg)
	if tr.TLSConfig() != cfg {
		t.Fatal("tls.Config should be stored verbatim")
	}
}

func TestTransport_RequiresVerifiedMTLS(t *testing.T) {
	tr := New(nil)
	ctx := context.Background()
	if _, err := tr.Dial(ctx, "peer", "127.0.0.1:443"); err != ErrTLSRequired {
		t.Fatalf("Dial err=%v, want ErrTLSRequired", err)
	}
	if _, err := tr.Listen(ctx, "127.0.0.1:0"); err != ErrTLSRequired {
		t.Fatalf("Listen err=%v, want ErrTLSRequired", err)
	}
}

func TestTransport_DirectMTLSStream(t *testing.T) {
	pki := newTestPKI(t)
	serverTransport := New(pki.serverTLS)
	clientTransport := New(pki.clientTLS)
	t.Cleanup(func() {
		_ = clientTransport.Close()
		_ = serverTransport.Close()
	})

	listener, err := serverTransport.Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := listener.(interface{ Addr() net.Addr }).Addr().String()

	serverDone := make(chan error, 1)
	serverPayload := make(chan []byte, 1)
	message := []byte("silentcut-direct")
	go func() {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		if conn.PeerID() != pki.clientID {
			serverDone <- &peerError{got: conn.PeerID(), want: pki.clientID}
			return
		}
		payload := make([]byte, len(message))
		_, err = io.ReadFull(conn, payload)
		if err != nil {
			serverDone <- err
			return
		}
		serverPayload <- payload
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := clientTransport.Dial(ctx, pki.serverID, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := conn.Write(message); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
	if payload := <-serverPayload; !bytes.Equal(payload, message) {
		t.Fatalf("payload=%q", payload)
	}
	_ = conn.Close()
}

func TestTransport_RelayCarriesOnlyQUICCiphertext(t *testing.T) {
	pki := newTestPKI(t)
	secret := []byte("0123456789abcdef0123456789abcdef")
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	recorder := &recordingPacketConn{PacketConn: udp}
	relayServer, err := relay.NewServer(recorder, relay.ServerConfig{
		Secret:             secret,
		RateBytesPerSecond: 100 * 1024 * 1024,
		BurstBytes:         16 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	relayCtx, stopRelay := context.WithCancel(context.Background())
	t.Cleanup(stopRelay)
	go func() { _ = relayServer.Serve(relayCtx) }()

	token, err := relay.IssueToken(secret, time.Minute, time.Now())
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	moonURI, _ := relay.URI(udp.LocalAddr().String(), token, relay.SideMoon)
	cometURI, _ := relay.URI(udp.LocalAddr().String(), token, relay.SideComet)

	serverTransport := NewWithConfig(pki.serverTLS, &quicgo.Config{HandshakeIdleTimeout: 3 * time.Second})
	clientTransport := NewWithConfig(pki.clientTLS, &quicgo.Config{HandshakeIdleTimeout: 3 * time.Second})
	t.Cleanup(func() {
		_ = clientTransport.Close()
		_ = serverTransport.Close()
	})
	listener, err := serverTransport.Listen(context.Background(), moonURI)
	if err != nil {
		t.Fatalf("relay Listen: %v", err)
	}

	marker := []byte("MP4-PLAINTEXT-MARKER-APP-510-DO-NOT-APPEAR-AT-RELAY")
	payload := append([]byte("\x00\x00\x00\x18ftypisom"), bytes.Repeat(marker, 512)...)
	serverHash := make(chan [32]byte, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		body := make([]byte, len(payload))
		_, err = io.ReadFull(conn, body)
		if err != nil {
			serverErr <- err
			return
		}
		serverHash <- sha256Bytes(body)
		serverErr <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := clientTransport.Dial(ctx, pki.serverID, cometURI)
	if err != nil {
		t.Fatalf("relay Dial: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("relay Write: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("relay server: %v", err)
	}
	_ = conn.Close()
	if got, want := <-serverHash, sha256Bytes(payload); got != want {
		t.Fatalf("received hash %x, want %x", got, want)
	}
	if relayServer.Stats().ForwardedBytes == 0 {
		t.Fatal("relay did not account for forwarded ciphertext")
	}
	for _, datagram := range recorder.snapshot() {
		if bytes.Contains(datagram, marker) {
			t.Fatal("relay-observed UDP datagram contained application plaintext")
		}
	}
}

type peerError struct {
	got, want mesh.PeerID
}

func (e *peerError) Error() string {
	return "peer id mismatch: got " + string(e.got) + ", want " + string(e.want)
}

type testPKI struct {
	serverTLS *tls.Config
	clientTLS *tls.Config
	serverID  mesh.PeerID
	clientID  mesh.PeerID
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "SoyaOS test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	serverCert, serverLeaf := issueTestCert(t, caCert, caKey, 2, "comet.test", x509.ExtKeyUsageServerAuth)
	clientCert, clientLeaf := issueTestCert(t, caCert, caKey, 3, "moon.test", x509.ExtKeyUsageClientAuth)
	return testPKI{
		serverTLS: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    pool,
			MinVersion:   tls.VersionTLS13,
		},
		clientTLS: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      pool,
			ServerName:   "comet.test",
			MinVersion:   tls.VersionTLS13,
		},
		serverID: PeerIDFromCertificate(serverLeaf),
		clientID: PeerIDFromCertificate(clientLeaf),
	}
}

func issueTestCert(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, serial int64, dns string, usage x509.ExtKeyUsage) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: dns},
		DNSNames:     []string{dns},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, public, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: private, Leaf: leaf}, leaf
}

type recordingPacketConn struct {
	net.PacketConn
	mu      sync.Mutex
	packets [][]byte
}

func (r *recordingPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, addr, err := r.PacketConn.ReadFrom(p)
	if n > 0 {
		r.mu.Lock()
		r.packets = append(r.packets, append([]byte(nil), p[:n]...))
		r.mu.Unlock()
	}
	return n, addr, err
}

func (r *recordingPacketConn) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([][]byte, len(r.packets))
	for i, packet := range r.packets {
		result[i] = append([]byte(nil), packet...)
	}
	return result
}

func sha256Bytes(data []byte) [32]byte {
	return sha256.Sum256(data)
}

package server

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/tftp"
)

// tftpSetup builds a Server whose TFTP server listens on a free loopback port, and an Object
// Manager socket that receives its answers. The port is held by blocker until the test frees it.
func tftpSetup(t *testing.T) (s *Server, om *net.UDPConn, blocker *net.UDPConn) {
	t.Helper()
	listen := func() *net.UDPConn {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	blocker, om, resp := listen(), listen(), listen()

	key := cipher.NewCipherKey([]byte("0123456789abcdef"), []byte("fedcba9876543210"))
	broadcastKey := cipher.NewCipherKey([]byte("bbbbbbbbbbbbbbbb"), []byte("iiiiiiiiiiiiiiii"))
	port := blocker.LocalAddr().(*net.UDPAddr).Port
	s = &Server{
		projectCipherKey:    key,
		broadcastCipherKeys: []*cipher.CipherKey{broadcastKey, key},
		unicastCipherKeys:   []*cipher.CipherKey{key},
		responseConn:        resp,
		tftpServer:          tftp.NewServer(t.TempDir(), "127.0.0.1", port, tftp.GetAndReplace),
	}
	t.Cleanup(s.tftpServer.Stop)
	return s, om, blocker
}

// answer returns what vCLU sent to Object Manager for a request already handled, "" for nothing.
func answer(t *testing.T, om *net.UDPConn, key *cipher.CipherKey) string {
	t.Helper()
	// The handler wrote before returning; on loopback the datagram is already queued
	_ = om.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, BufferSize)
	n, _, err := om.ReadFromUDP(buf)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	plain, err := key.Decrypt(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}

// send encrypts req and hands it to the command dispatch as if it came from Object Manager.
func send(t *testing.T, s *Server, om *net.UDPConn, key *cipher.CipherKey, req string, broadcast bool) {
	t.Helper()
	data, err := key.Encrypt([]byte(req))
	if err != nil {
		t.Fatal(err)
	}
	from := om.LocalAddr().(*net.UDPAddr)
	if broadcast {
		s.processBroadcastCommand(data, from)
	} else {
		s.processUnicastCommand(data, from)
	}
}

// Object Manager asks a CLU to start its TFTP server before sending a project, with
// req_start_ftp (broadcast key) or StartTFTPd (project key). vCLU answers OK only when the
// server really listens, so a taken port is reported and the next request tries again.
func TestStartTFTPRequestsAnswerOKOnlyWhenListening(t *testing.T) {
	for _, tc := range []struct {
		name      string
		broadcast bool
		key       func(s *Server) *cipher.CipherKey
	}{
		{"req_start_ftp", true, func(s *Server) *cipher.CipherKey { return s.broadcastCipherKeys[0] }},
		{"StartTFTPd", false, func(s *Server) *cipher.CipherKey { return s.projectCipherKey }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, om, blocker := tftpSetup(t)
			key := tc.key(s)

			send(t, s, om, key, "req_start_ftp\r\n", tc.broadcast)
			if got := answer(t, om, key); got != "" {
				t.Fatalf("port taken: answered %q, want no answer", got)
			}
			if s.tftpServer.IsRunning() {
				t.Fatal("port taken: TFTP server reported as running")
			}

			_ = blocker.Close()
			send(t, s, om, key, "req_start_ftp\r\n", tc.broadcast)
			if got := answer(t, om, key); got != "resp:OK" {
				t.Fatalf("port free: answered %q, want resp:OK", got)
			}
			if !s.tftpServer.IsRunning() {
				t.Fatal("port free: TFTP server not running after OK")
			}

			// A repeated request while running is answered OK as before
			send(t, s, om, key, "req_start_ftp\r\n", tc.broadcast)
			if got := answer(t, om, key); got != "resp:OK" {
				t.Fatalf("running: answered %q, want resp:OK", got)
			}
		})
	}
}

package tftp

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pin/tftp/v3"
)

// takePort binds a free loopback UDP port and returns it with the socket holding it.
func takePort(t *testing.T) (int, *net.UDPConn) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().(*net.UDPAddr).Port, conn
}

// stopWithin fails the test if Stop blocks.
func stopWithin(t *testing.T, s *Server) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() blocked")
	}
}

// A taken port makes Start fail and leaves the server stopped; once the port is free, Start works.
func TestStartReportsTakenPort(t *testing.T) {
	port, blocker := takePort(t)
	s := NewServer(t.TempDir(), "127.0.0.1", port, GetAndReplace)

	if err := s.Start(); err == nil {
		s.Stop()
		t.Fatal("Start() on a taken port returned no error")
	}
	if s.IsRunning() {
		t.Fatal("IsRunning() is true after a failed start")
	}
	stopWithin(t, s)

	_ = blocker.Close()
	if err := s.Start(); err != nil {
		t.Fatalf("Start() once the port is free: %v", err)
	}
	if !s.IsRunning() {
		t.Fatal("IsRunning() is false after a successful start")
	}
	stopWithin(t, s)
	if s.IsRunning() {
		t.Fatal("IsRunning() is true after Stop()")
	}
}

// A file Object Manager writes after a successful start lands in the root dir; Stop frees the port.
func TestWriteAfterStartLandsInRootDir(t *testing.T) {
	port, blocker := takePort(t)
	_ = blocker.Close()
	root := t.TempDir()
	s := NewServer(root, "127.0.0.1", port, GetAndReplace)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}

	c, err := tftp.NewClient("127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("-- project\n")
	rf, err := c.Send(`a:\OM.LUA`, "octet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rf.ReadFrom(bytes.NewReader(want)); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(root, "a", "om.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("om.lua = %q, want %q", got, want)
	}

	stopWithin(t, s)
	// The port is free again, as before the next upload
	if err := s.Start(); err != nil {
		t.Fatalf("Start() after Stop(): %v", err)
	}
	stopWithin(t, s)
}

// Concurrent Start/Stop/IsRunning, as from the UDP handlers and the reset; run with -race.
func TestStartStopConcurrent(t *testing.T) {
	port, blocker := takePort(t)
	_ = blocker.Close()
	s := NewServer(t.TempDir(), "127.0.0.1", port, GetAndReplace)
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				_ = s.Start()
				_ = s.IsRunning()
				s.Stop()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	stopWithin(t, s)
}

// A Stop waiting for a stalled upload must not hold IsRunning and a new Start: the UDP command
// handlers call them, and two blocked handlers stop all Grenton commands.
func TestStopWaitingForStalledUploadDoesNotBlock(t *testing.T) {
	port, held := takePort(t)
	_ = held.Close()
	s := NewServer(t.TempDir(), "127.0.0.1", port, GetAndReplace)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}

	// a client that sends a write request, reads ACK 0, then goes silent
	cl, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	wrq := append([]byte{0, 2}, []byte("a:\\OM.LUA\x00octet\x00")...)
	if _, err := cl.WriteToUDP(wrq, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 600)
	if err := cl.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, _, err := cl.ReadFromUDP(buf); err != nil || n < 4 || buf[1] != 4 {
		t.Fatalf("no ACK 0: n=%d err=%v", n, err)
	}

	go s.Stop() // waits for the stalled transfer
	time.Sleep(200 * time.Millisecond)

	done := make(chan bool)
	go func() {
		running := s.IsRunning()
		err := s.Start()
		done <- !running && err == nil
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Error("during the stop: want IsRunning false and a new Start to succeed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("IsRunning/Start blocked while Stop waited for a stalled upload")
	}
	// the restarted server; the first Stop still waits for the stalled client
	t.Cleanup(func() { go s.Stop() })
}

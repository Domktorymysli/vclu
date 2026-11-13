package server

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
)

// A CLU answered a script of 1443 bytes from 192.168.0.50 (1472 bytes encrypted) and did
// not answer 1453 (1488 bytes, fragmented). The largest script that fits is sent; one byte
// more is refused at once, naming the limit, instead of timing out.
func TestSendCommandSizeLimit(t *testing.T) {
	key := cipher.NewCipherKey([]byte("0123456789abcdef"), []byte("fedcba9876543210"))
	port := fakeCLU(t, key, func(script string) string {
		return fmt.Sprintf("resp:127.0.0.1:00000000:%d", len(script))
	})
	s := &Server{localIP: net.IPv4(192, 168, 0, 50), projectCipherKey: key, cluPort: port}

	fits := `#"` + strings.Repeat("x", 1440) + `"`
	got, err := s.SendCommandWithTimeout("127.0.0.1", fits, time.Second)
	if err != nil || got != "1443" {
		t.Fatalf("1443 bytes: %q, %v", got, err)
	}

	tooLong := fits + " "
	start := time.Now()
	_, err = s.SendCommandWithTimeout("127.0.0.1", tooLong, time.Second)
	if err == nil || !strings.Contains(err.Error(), "script too long for a CLU: 1444 bytes, at most 1443") {
		t.Errorf("1444 bytes: %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("refused after %v, want at once", time.Since(start))
	}
	if err := s.SendCommandNoReply("127.0.0.1", tooLong); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("no reply, 1444 bytes: %v", err)
	}
}

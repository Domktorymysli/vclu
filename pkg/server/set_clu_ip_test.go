package server

import (
	"bytes"
	"net"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/config"
)

// setCluIPSetup builds a Server at 192.168.50.57 that answers req_set_clu_ip, and an Object
// Manager socket that receives its answers.
func setCluIPSetup(t *testing.T) (s *Server, om *net.UDPConn) {
	t.Helper()
	listen := func() *net.UDPConn {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	om, resp := listen(), listen()

	key := cipher.NewCipherKey([]byte("0123456789abcdef"), []byte("fedcba9876543210"))
	broadcastKey := cipher.NewCipherKey([]byte("bbbbbbbbbbbbbbbb"), []byte("iiiiiiiiiiiiiiii"))
	s = &Server{
		deviceConfig:        &config.CLUDeviceConfig{SerialNumber: 0x0d2d452b},
		localIP:             net.IPv4(192, 168, 50, 57),
		projectCipherKey:    key,
		broadcastCipherKeys: []*cipher.CipherKey{broadcastKey, key},
		unicastCipherKeys:   []*cipher.CipherKey{key},
		responseConn:        resp,
	}
	return s, om
}

// Object Manager 1.13 picks a CLU's address from the project's IP range and asks the CLU to move
// there, by unicast first and then by broadcast. vCLU cannot move: it confirms only its own
// address. A confirmation of another one made Object Manager put the CLU at that address and send
// SetKey there ("Host is down", "Receive timed out"); "null" is what it asks for when the range
// has no free address.
func TestSetCluIPConfirmsOnlyOwnAddress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		broadcast bool
		key       func(s *Server) *cipher.CipherKey
		want      string
	}{
		{"broadcast", true, func(s *Server) *cipher.CipherKey { return s.broadcastCipherKeys[0] },
			"resp_set_clu_ip:0d2d452b:192.168.50.57\r\n"},
		{"unicast", false, func(s *Server) *cipher.CipherKey { return s.projectCipherKey },
			"resp_set_clu_ip:000000000d2d452b:192.168.50.57"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, om := setCluIPSetup(t)
			key := tc.key(s)

			send(t, s, om, key, "req_set_clu_ip:0d2d452b:192.168.50.57:192.168.50.1\r\n", tc.broadcast)
			if got := answer(t, om, key); got != tc.want {
				t.Fatalf("own address: answered %q, want %q", got, tc.want)
			}

			for _, ip := range []string{"192.168.50.2", "null"} {
				send(t, s, om, key, "req_set_clu_ip:0d2d452b:"+ip+":192.168.50.1\r\n", tc.broadcast)
				if got := answer(t, om, key); got != "" {
					t.Fatalf("asked for %s: answered %q, want no answer", ip, got)
				}
			}
		})
	}
}

// Another CLU's serial is not vCLU's business, even with vCLU's own address.
func TestSetCluIPOtherSerialUnanswered(t *testing.T) {
	s, om := setCluIPSetup(t)
	key := s.broadcastCipherKeys[0]
	send(t, s, om, key, "req_set_clu_ip:0d000001:192.168.50.57:192.168.50.1\r\n", true)
	if got := answer(t, om, key); got != "" {
		t.Fatalf("other serial: answered %q, want no answer", got)
	}
}

// A message encrypted with the right key and another IV, as in a report from Object Manager 1.13
// ("unknown command ... <16 garbage bytes>d2d452b:192.168.50.57:192.168.50.1"), gets a hint
// in the log; readable or wholly unreadable data does not.
func TestWrongIVHint(t *testing.T) {
	key := []byte("0123456789abcdef")
	// IVs that differ in the top bit of every byte: the first block comes out as non-ASCII
	sent := cipher.NewCipherKey(key, make([]byte, 16))
	read := cipher.NewCipherKey(key, bytes.Repeat([]byte{0x80}, 16))
	data, err := sent.Encrypt([]byte("req_set_clu_ip:0d2d452b:192.168.50.57:192.168.50.1\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	garbled, err := read.Decrypt(data)
	if err != nil {
		t.Fatal(err)
	}
	if wrongIVHint(garbled) == "" {
		t.Errorf("no hint for %q", garbled)
	}
	if h := wrongIVHint([]byte("req_set_clu_ip:0d2d452b:192.168.50.57:192.168.50.1\r\n")); h != "" {
		t.Errorf("hint for readable data: %q", h)
	}
	if h := wrongIVHint(bytes.Repeat([]byte{0xff}, 48)); h != "" {
		t.Errorf("hint for unreadable data: %q", h)
	}
}

// discoverIV sends req_discovery_clu the way Object Manager does (the broadcast key with its
// fixed IV around the request, ownIV inside) and returns the temporary IV of vCLU's answer.
func discoverIV(t *testing.T, s *Server, om *net.UDPConn, ownIV []byte) []byte {
	t.Helper()
	var req bytes.Buffer
	req.Write(bytes.Repeat([]byte{0x5a}, 32))
	req.WriteByte(':')
	req.Write(ownIV)
	req.WriteString(":req_discovery_clu:127.0.0.1\r\n")
	data, err := cipher.DefaultBroadcastKey.Encrypt(req.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s.processBroadcastCommand(data, om.LocalAddr().(*net.UDPAddr))
	got := []byte(answer(t, om, cipher.DefaultBroadcastKey.WithIV(ownIV)))
	// encrypted(32):temporaryIV(16):resp_discovery_clu:...
	if len(got) < 49 || !bytes.Contains(got, []byte("resp_discovery_clu")) {
		t.Fatalf("discovery answered %q", got)
	}
	return got[33:49]
}

// Object Manager encrypts req_set_clu_ip with the temporary key from the discovery answer it
// read. With a new temporary IV at every discovery, a second discovery shortly after the first
// (two network interfaces, a retry) left vCLU with another IV, and the request read as "<16
// garbage bytes>d2d452b:192.168.50.57:192.168.50.1": unknown command, no answer. The IV now lasts
// until SetKey.
func TestSetCluIPAfterTwoDiscoveries(t *testing.T) {
	s, om := setCluIPSetup(t)
	oldBroadcast, oldKey, oldBroadcastIV, oldIV := cipher.DefaultBroadcastKey, cipher.DefaultKey, cipher.DefaultBroadcastIV, cipher.DefaultIV
	t.Cleanup(func() {
		cipher.DefaultBroadcastKey, cipher.DefaultKey, cipher.DefaultBroadcastIV, cipher.DefaultIV = oldBroadcast, oldKey, oldBroadcastIV, oldIV
	})
	cipher.SetDefaultBroadcastKey([]byte("bbbbbbbbbbbbbbbb"), []byte("iiiiiiiiiiiiiiii"))
	cipher.SetDefaultIV([]byte("dddddddddddddddd"))
	pin := []byte("00000000")
	s.keys = &config.CLUKeys{PIN: pin}
	s.macAddr = net.HardwareAddr{0x02, 0, 0, 0, 0, 1}
	s.broadcastCipherKeys = []*cipher.CipherKey{cipher.DefaultBroadcastKey, s.projectCipherKey}

	first := discoverIV(t, s, om, []byte("0123456789ABCDEF"))
	second := discoverIV(t, s, om, []byte("0123456789ABCDEF"))
	if !bytes.Equal(first, second) {
		t.Errorf("temporary IV changed between discoveries: %x, then %x", first, second)
	}

	// Unicast: the temporary key comes first there. On broadcast the broadcast key is tried first
	// and about one ciphertext in 256 has valid padding under it too, which would make the test flaky.
	omKey := cipher.GetInitialCipherKey(first, pin)
	send(t, s, om, omKey, "req_set_clu_ip:0d2d452b:192.168.50.57:192.168.50.1\r\n", false)
	if got, want := answer(t, om, omKey), "resp_set_clu_ip:000000000d2d452b:192.168.50.57"; got != want {
		t.Fatalf("set IP with the first answer's key: answered %q, want %q", got, want)
	}

	// SetKey ends the temporary key: the next discovery gets a new IV
	s.serverDir = t.TempDir()
	newKey, newIV := []byte("pppppppppppppppp"), []byte("qqqqqqqqqqqqqqqq")
	var setKey bytes.Buffer
	setKey.Write(bytes.Repeat([]byte{0x5a}, 32))
	setKey.WriteByte(':')
	setKey.Write(newIV)
	setKey.WriteString(":req_set_key:")
	setKey.Write(newKey)
	send(t, s, om, omKey, setKey.String(), false)
	if got := answer(t, om, cipher.NewCipherKey(newKey, newIV)); got != "resp:OK" {
		t.Fatalf("SetKey: answered %q, want resp:OK", got)
	}
	if third := discoverIV(t, s, om, []byte("0123456789ABCDEF")); bytes.Equal(third, first) {
		t.Errorf("temporary IV kept after SetKey: %x", third)
	}
}

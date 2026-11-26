package lua

import (
	"net"
	"net/netip"
	"testing"
	"time"
)

// A plugin's UDP socket hears answers only: from an address it sent to, or from the port it
// broadcast to on the local network. Anyone else who finds its port is not heard.
func TestPluginUDPHearsOnlyAnswers(t *testing.T) {
	pio := NewPluginIO()
	pio.SetGrant("@t/u", pluginIOGrant{udp: true, udpPorts: []int{7000}, broadcastPorts: []int{7000}})
	id, err := pio.OpenDeviceUDP("@t/u", 0)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := pio.handle(id, "@t/u")
	from := func(ip string, port int) bool {
		return pio.answer(h, net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr(ip), uint16(port))))
	}
	if from("192.168.1.20", 7000) || from("8.8.8.8", 7000) {
		t.Error("heard before sending anything")
	}
	pio.mu.Lock()
	h.peers[netip.MustParseAddr("192.168.1.20")] = true
	h.bcast[7000] = time.Now()
	pio.mu.Unlock()
	if !from("192.168.1.20", 7000) || !from("192.168.1.20", 41000) {
		t.Error("the device it sent to not heard")
	}
	if !from("192.168.1.31", 7000) {
		t.Error("an answer to its broadcast not heard")
	}
	if from("192.168.1.31", 7001) || from("8.8.8.8", 7000) {
		t.Error("heard a stranger")
	}
	pio.mu.Lock()
	h.bcast[7000] = time.Now().Add(-2 * broadcastAnswers)
	pio.mu.Unlock()
	if from("192.168.1.31", 7000) {
		t.Error("an answer long after the broadcast heard")
	}
	// vclu's listener knows the open socket's datagrams however long it waited
	port := h.conn.LocalAddr().(*net.UDPAddr).Port
	pio.mu.Lock()
	pio.sent = map[int]time.Time{}
	pio.mu.Unlock()
	if !pio.FromPlugin(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}) {
		t.Error("an open socket's datagram not known as the plugin's")
	}
	if _, err := pio.OpenDeviceUDP("@t/u", 7000); err == nil {
		t.Error("bound a chosen port")
	}
}

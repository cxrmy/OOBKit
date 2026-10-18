package main

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/krolaw/dhcp4"
)

func newTestServer() *DHCPServer {
	mask := net.IPMask(net.ParseIP("255.255.255.0").To4())
	srv := &DHCPServer{
		serverIP:    net.ParseIP("192.168.1.1").To4(),
		netMask:     mask,
		bcastIP:     broadcastIP(net.ParseIP("192.168.1.1").To4(), mask),
		startIP:     net.ParseIP("192.168.1.2").To4(),
		endIP:       net.ParseIP("192.168.1.10").To4(),
		gateway:     net.ParseIP("192.168.1.1").To4(),
		leases:      make(map[string]DHCPLease),
		usedIPs:     make(map[string]bool),
		emitLog:     func(string) {},
		emitDHCPLog: func(string) {},
	}
	return srv
}

func reqPacket(t *testing.T, mt dhcp4.MessageType, mac net.HardwareAddr, requested net.IP) dhcp4.Packet {
	t.Helper()
	p := dhcp4.NewPacket(dhcp4.BootRequest)
	p.SetXId([]byte{0x5A, 0x5A, 0x5A, 0x5A})
	p.SetCHAddr(mac)
	p.AddOption(dhcp4.OptionDHCPMessageType, []byte{byte(mt)})
	if requested != nil {
		p.AddOption(dhcp4.OptionRequestedIPAddress, requested.To4())
	}
	return p
}

func TestBroadcastIP(t *testing.T) {
	cases := []struct{ ip, mask, want string }{
		{"192.168.1.1", "255.255.255.0", "192.168.1.255"},
		{"10.1.2.3", "255.255.0.0", "10.1.255.255"},
		{"172.16.5.9", "255.255.255.252", "172.16.5.11"},
	}
	for _, c := range cases {
		got := broadcastIP(net.ParseIP(c.ip).To4(), net.IPMask(net.ParseIP(c.mask).To4())).String()
		if got != c.want {
			t.Fatalf("broadcastIP(%s,%s)=%s want %s", c.ip, c.mask, got, c.want)
		}
	}
}

func TestAllocRespectsPoolBounds(t *testing.T) {
	s := newTestServer()
	s.startIP = net.ParseIP("192.168.1.8").To4()
	s.endIP = net.ParseIP("192.168.1.10").To4()

	s.mu.Lock()
	defer s.mu.Unlock()
	var got []string
	for {
		ip := s.allocLocked()
		if ip == nil {
			break
		}
		got = append(got, ip.String())
		s.usedIPs[ip.String()] = true
	}
	want := []string{"192.168.1.8", "192.168.1.9", "192.168.1.10"}
	if len(got) != len(want) {
		t.Fatalf("allocated %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("allocated %v, want %v", got, want)
		}
	}
}

func TestAllocSkipsServerAndBroadcast(t *testing.T) {
	s := newTestServer()
	s.startIP = net.ParseIP("192.168.1.1").To4()
	s.endIP = net.ParseIP("192.168.1.255").To4()
	s.mu.Lock()
	ip := s.allocLocked()
	s.mu.Unlock()
	if ip.String() != "192.168.1.2" {
		t.Fatalf("first alloc = %s, want 192.168.1.2 (skip server .1)", ip)
	}
}

func TestExpiredLeaseIsRecycled(t *testing.T) {
	s := newTestServer()
	s.startIP = net.ParseIP("192.168.1.2").To4()
	s.endIP = net.ParseIP("192.168.1.2").To4()

	s.mu.Lock()
	s.leases["aa:aa:aa:aa:aa:aa"] = DHCPLease{IP: "192.168.1.2", MAC: "aa:aa:aa:aa:aa:aa", Expires: time.Now().Add(-time.Minute)}
	s.usedIPs["192.168.1.2"] = true
	ip := s.allocLocked()
	s.mu.Unlock()
	if ip == nil || ip.String() != "192.168.1.2" {
		t.Fatalf("expired lease not recycled, got %v", ip)
	}
}

func TestDiscoverOfferAndAck(t *testing.T) {
	s := newTestServer()
	mac := net.HardwareAddr{0xB0, 0x25, 0xAA, 0x52, 0x76, 0xAE}

	// DHCPDISCOVER → OFFER
	disc := reqPacket(t, dhcp4.Discover, mac, nil)
	res := s.ServeDHCP(disc, dhcp4.Discover, disc.ParseOptions())
	if res == nil {
		t.Fatal("no OFFER for DISCOVER")
	}
	if mt := res.ParseOptions()[dhcp4.OptionDHCPMessageType]; mt[0] != byte(dhcp4.Offer) {
		t.Fatalf("message type = %d, want OFFER", mt[0])
	}
	offered := res.YIAddr().String()
	if offered != "192.168.1.2" {
		t.Fatalf("offered %s, want 192.168.1.2", offered)
	}
	opts := res.ParseOptions()
	if m := net.IPMask(opts[dhcp4.OptionSubnetMask]); !bytes.Equal(m, net.IPMask(net.ParseIP("255.255.255.0").To4())) {
		t.Fatalf("subnet mask option = %v", m)
	}
	if gw := net.IP(opts[dhcp4.OptionRouter]).String(); gw != "192.168.1.1" {
		t.Fatalf("router option = %s", gw)
	}
	if lt := opts[dhcp4.OptionIPAddressLeaseTime]; len(lt) != 4 {
		t.Fatal("missing lease time option")
	}
	if sid := net.IP(opts[dhcp4.OptionServerIdentifier]).String(); sid != "192.168.1.1" {
		t.Fatalf("server identifier = %s", sid)
	}

	// 同一 MAC 再来一次 DISCOVER → 还是同一个地址
	res2 := s.ServeDHCP(disc, dhcp4.Discover, disc.ParseOptions())
	if res2.YIAddr().String() != offered {
		t.Fatalf("re-discover changed ip: %s vs %s", res2.YIAddr(), offered)
	}

	// DHCPREQUEST 请求该地址 → ACK
	req := reqPacket(t, dhcp4.Request, mac, net.ParseIP(offered))
	ack := s.ServeDHCP(req, dhcp4.Request, req.ParseOptions())
	if ack == nil || ack.ParseOptions()[dhcp4.OptionDHCPMessageType][0] != byte(dhcp4.ACK) {
		t.Fatalf("expected ACK, got %v", ack)
	}
	if ack.YIAddr().String() != offered {
		t.Fatalf("ack yiaddr %s", ack.YIAddr())
	}
}

func TestRequestWrongIPGetsNAK(t *testing.T) {
	s := newTestServer()
	mac := net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}

	disc := reqPacket(t, dhcp4.Discover, mac, nil)
	s.ServeDHCP(disc, dhcp4.Discover, disc.ParseOptions())

	req := reqPacket(t, dhcp4.Request, mac, net.ParseIP("192.168.9.99"))
	nak := s.ServeDHCP(req, dhcp4.Request, req.ParseOptions())
	if nak == nil {
		t.Fatal("expected NAK")
	}
	if mt := nak.ParseOptions()[dhcp4.OptionDHCPMessageType][0]; mt != byte(dhcp4.NAK) {
		t.Fatalf("message type = %d, want NAK", mt)
	}
}

func TestReleaseFreesLease(t *testing.T) {
	s := newTestServer()
	mac := net.HardwareAddr{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x01}

	disc := reqPacket(t, dhcp4.Discover, mac, nil)
	res := s.ServeDHCP(disc, dhcp4.Discover, disc.ParseOptions())
	ip := res.YIAddr().String()

	rel := reqPacket(t, dhcp4.Release, mac, nil)
	s.ServeDHCP(rel, dhcp4.Release, rel.ParseOptions())

	if _, ok := s.leases[mac.String()]; ok {
		t.Fatal("lease not removed after RELEASE")
	}
	if s.usedIPs[ip] {
		t.Fatal("usedIPs not cleared after RELEASE")
	}
}

func TestLeaseRenewExtendsExpiry(t *testing.T) {
	s := newTestServer()
	mac := net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x09}
	disc := reqPacket(t, dhcp4.Discover, mac, nil)
	s.ServeDHCP(disc, dhcp4.Discover, disc.ParseOptions())

	s.mu.Lock()
	before := s.leases[mac.String()].Expires
	s.mu.Unlock()

	time.Sleep(10 * time.Millisecond)
	req := reqPacket(t, dhcp4.Request, mac, nil)
	s.ServeDHCP(req, dhcp4.Request, req.ParseOptions())

	s.mu.Lock()
	after := s.leases[mac.String()].Expires
	s.mu.Unlock()
	if !after.After(before) {
		t.Fatalf("expiry not extended: %v -> %v", before, after)
	}
}

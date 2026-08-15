package main

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/krolaw/dhcp4"
)

type DHCPServer struct {
	mu         sync.Mutex
	conn       net.PacketConn
	running    bool
	serverIP   net.IP
	startIP    net.IP
	endIP      net.IP
	gateway    net.IP
	netMask    net.IPMask
	leases     map[string]DHCPLease
	usedIPs    map[string]bool
	emitLog    func(string)
	emitDHCPLog func(string)
}

type DHCPLease struct {
	IP      string
	MAC     string
	Expires time.Time
}

// ServeDHCP 实现 dhcp4.Handler 接口
func (s *DHCPServer) ServeDHCP(pkt dhcp4.Packet, msgType dhcp4.MessageType, _ dhcp4.Options) dhcp4.Packet {
	mac := pkt.CHAddr().String()

	s.mu.Lock()
	var assignedIP net.IP
	if l, ok := s.leases[mac]; ok {
		assignedIP = net.ParseIP(l.IP).To4()
	}
	if assignedIP == nil {
		assignedIP = s.alloc()
		if assignedIP == nil {
			s.mu.Unlock()
			s.emitDHCPLog(fmt.Sprintf("IP池耗尽: %s", mac))
			return nil
		}
		s.leases[mac] = DHCPLease{IP: assignedIP.String(), MAC: mac, Expires: time.Now().Add(24 * time.Hour)}
		s.usedIPs[assignedIP.String()] = true
	}
	s.mu.Unlock()

	switch msgType {
	case dhcp4.Discover:
		s.emitDHCPLog(fmt.Sprintf("Offer: %s → %s", mac, assignedIP))
		return dhcp4.ReplyPacket(pkt, dhcp4.Offer, s.serverIP.To4(), assignedIP, 24*time.Hour,
			[]dhcp4.Option{
				{Code: dhcp4.OptionSubnetMask, Value: []byte(s.netMask)},
				{Code: dhcp4.OptionRouter, Value: s.gateway.To4()},
				{Code: dhcp4.OptionDomainNameServer, Value: s.gateway.To4()},
			})
	case dhcp4.Request:
		s.emitDHCPLog(fmt.Sprintf("分配: %s → %s", mac, assignedIP))
		return dhcp4.ReplyPacket(pkt, dhcp4.ACK, s.serverIP.To4(), assignedIP, 24*time.Hour,
			[]dhcp4.Option{
				{Code: dhcp4.OptionSubnetMask, Value: []byte(s.netMask)},
				{Code: dhcp4.OptionRouter, Value: s.gateway.To4()},
				{Code: dhcp4.OptionDomainNameServer, Value: s.gateway.To4()},
			})
	}
	return nil
}

func (a *App) StartDHCPServer(ifaceName, startIP, endIP, mask, gateway string) map[string]interface{} {
	if a.dhcpServer != nil && a.dhcpServer.running {
		return map[string]interface{}{"success": false, "message": "DHCP 服务已在运行"}
	}

	ipStart := net.ParseIP(startIP).To4()
	ipEnd := net.ParseIP(endIP).To4()
	ipMask := net.IPMask(net.ParseIP(mask).To4())
	ipGW := net.ParseIP(gateway).To4()
	if ipStart == nil || ipEnd == nil || ipMask == nil {
		return map[string]interface{}{"success": false, "message": "IP 地址格式无效"}
	}

	var serverIP net.IP
	if iface, err := net.InterfaceByName(ifaceName); err == nil {
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
					serverIP = n.IP.To4()
					break
				}
			}
		}
	}
	if serverIP == nil {
		serverIP = net.IPv4(192, 168, 1, 1)
	}

	conn, err := newUDP67Conn(serverIP)
	if err != nil {
		msg := "端口 67 被占用，请检查是否有其他 DHCP 服务在运行"
		return map[string]interface{}{"success": false, "message": msg}
	}

	srv := &DHCPServer{
		conn: conn, running: true, serverIP: serverIP,
		startIP: ipStart, endIP: ipEnd, gateway: ipGW, netMask: ipMask,
		leases: make(map[string]DHCPLease), usedIPs: make(map[string]bool),
		emitLog: a.emitLog, emitDHCPLog: a.emitDHCPLog,
	}
	a.dhcpServer = srv

	go func() {
		srv.emitLog(fmt.Sprintf("DHCP 已启动: %s~%s (服务器: %s)", startIP, endIP, serverIP))
		if err := dhcp4.Serve(conn, srv); err != nil {
			if srv.running {
				srv.emitLog("DHCP 异常: " + err.Error())
			}
		}
		srv.running = false
	}()

	return map[string]interface{}{"success": true, "message": "DHCP 已启动"}
}

func (a *App) StopDHCPServer() {
	if a.dhcpServer != nil {
		a.dhcpServer.running = false
		a.dhcpServer.conn.Close()
		a.dhcpServer = nil
		a.emitLog("DHCP 已停止")
	}
}

func (a *App) GetDHCPLeases() []map[string]interface{} {
	if a.dhcpServer == nil {
		return nil
	}
	a.dhcpServer.mu.Lock()
	defer a.dhcpServer.mu.Unlock()
	var result []map[string]interface{}
	for _, l := range a.dhcpServer.leases {
		result = append(result, map[string]interface{}{
			"mac":     l.MAC,
			"ip":      l.IP,
			"expires": l.Expires.Format("15:04:05"),
		})
	}
	return result
}

func (s *DHCPServer) alloc() net.IP {
	cur := make(net.IP, 4)
	copy(cur, s.startIP)
	for i := 0; i < 254; i++ {
		ipStr := cur.String()
		if !s.usedIPs[ipStr] && ipStr != s.serverIP.String() {
			res := make(net.IP, 4)
			copy(res, cur)
			return res
		}
		for j := 3; j >= 0; j-- {
			cur[j]++
			if cur[j] != 0 {
				break
			}
		}
	}
	return nil
}

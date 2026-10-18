package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/google/gopacket/pcap"
	"github.com/krolaw/dhcp4"
)

// ─── DHCP 服务器 ──────────────────────────────────────────────────
//
// 相比「直接套 dhcp4.ListenAndServe」多做了几件事：
//   1. 服务端 IP 不再写死 192.168.1.1：按所选网卡的实际 IPv4 取，
//      并校验它与地址池同网段；
//   2. 应答不再一律发往 255.255.255.255（Windows 会按默认路由选网卡，
//      多网卡时经常从 WLAN 发出去，BMC 根本收不到），而是发往本网段的
//      定向广播（如 192.168.1.255），发送失败再退回有限广播；
//   3. 地址池严格限制在「起始~结束」之间，跳过网络地址/广播地址/本机地址，
//      并回收过期与 DHCPRELEASE/Decline 的租约；
//   4. DHCPREQUEST 请求的地址与分配不一致时回 NAK，让客户端重新申请；
//   5. 单次发送失败不会让整个服务退出。

const dhcpLeaseDuration = 24 * time.Hour

type DHCPLease struct {
	IP      string
	MAC     string
	Expires time.Time
}

type DHCPServer struct {
	mu          sync.Mutex
	conn        net.PacketConn
	running     bool
	serverIP    net.IP
	netMask     net.IPMask
	bcastIP     net.IP
	startIP     net.IP
	endIP       net.IP
	gateway     net.IP
	leases      map[string]DHCPLease
	usedIPs     map[string]bool
	emitLog     func(string)
	emitDHCPLog func(string)
}

// ─── 启停 ─────────────────────────────────────────────────────────

func (a *App) StartDHCPServer(ifaceName, startIP, endIP, mask, gateway string) map[string]interface{} {
	if a.dhcpServer != nil && a.dhcpServer.isRunning() {
		return map[string]interface{}{"success": false, "message": "DHCP 服务已在运行，请先停止"}
	}

	ipStart := net.ParseIP(startIP).To4()
	ipEnd := net.ParseIP(endIP).To4()
	maskIP := net.ParseIP(mask).To4()
	if ipStart == nil || ipEnd == nil || maskIP == nil {
		return map[string]interface{}{"success": false, "message": "IP / 掩码格式无效"}
	}
	if !validIPv4Mask(mask) {
		return map[string]interface{}{"success": false, "message": "子网掩码无效: " + mask}
	}
	ipMask := net.IPMask(maskIP)
	ones, _ := ipMask.Size()

	if binary.BigEndian.Uint32(ipEnd) < binary.BigEndian.Uint32(ipStart) {
		return map[string]interface{}{"success": false, "message": "起始 IP 不能大于结束 IP"}
	}
	poolNet := ipStart.Mask(ipMask)
	if !ipEnd.Mask(ipMask).Equal(poolNet) {
		return map[string]interface{}{"success": false, "message": "起始 IP 与结束 IP 不在同一网段"}
	}

	// 服务端 IP 取所选网卡的真实地址（原来这里用 net.InterfaceByName(pcap 设备名)
	// 永远查不到，最后 fallback 成 192.168.1.1，导致非 192.168.1.x 环境下全部不可用）
	serverIP, ok := ifaceIPv4ForSubnet(ifaceName, ipStart, ipMask)
	if !ok {
		return map[string]interface{}{
			"success": false,
			"message": "所选网卡没有 IPv4 地址，请先给网卡配置与地址池同网段的 IP（可点「初始化」）",
		}
	}
	if !serverIP.Mask(ipMask).Equal(poolNet) {
		return map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("网卡 IP %s 与地址池 %s 不在同一网段（掩码 %s），请先修正网卡 IP 或地址池",
				serverIP, startIP, mask),
		}
	}
	if serverIP.Equal(ipStart) || serverIP.Equal(ipEnd) {
		return map[string]interface{}{"success": false, "message": "本机 IP 不能与地址池首尾地址相同"}
	}

	var ipGW net.IP
	if gateway != "" {
		ipGW = net.ParseIP(gateway).To4()
		if ipGW == nil {
			return map[string]interface{}{"success": false, "message": "网关地址格式无效: " + gateway}
		}
	}

	bcast := broadcastIP(serverIP, ipMask)

	// 清掉残留的旧实例，避免它继续占着 67 端口
	if a.dhcpServer != nil {
		a.dhcpServer.stop()
		a.dhcpServer = nil
	}

	conn, err := newUDP67Conn(serverIP)
	if err != nil {
		return map[string]interface{}{
			"success": false,
			"message": "端口 67 被占用，请检查是否有其他 DHCP 服务在运行: " + err.Error(),
		}
	}

	srv := &DHCPServer{
		conn: conn, running: true,
		serverIP: serverIP, netMask: ipMask, bcastIP: bcast,
		startIP: ipStart, endIP: ipEnd, gateway: ipGW,
		leases: make(map[string]DHCPLease), usedIPs: make(map[string]bool),
		emitLog: a.emitLog, emitDHCPLog: a.emitDHCPLog,
	}
	a.dhcpServer = srv

	a.emitLog(fmt.Sprintf("DHCP 启动: 服务端 %s/%d，地址池 %s ~ %s，广播 %s",
		serverIP, ones, startIP, endIP, bcast))

	go func() {
		err := srv.serve()
		srv.mu.Lock()
		srv.running = false
		srv.mu.Unlock()
		if err != nil {
			srv.emitDHCPLog("DHCP 服务已停止: " + err.Error())
		}
	}()

	return map[string]interface{}{
		"success":   true,
		"message":   fmt.Sprintf("DHCP 已启动：%s/%d，地址池 %s ~ %s", serverIP, ones, startIP, endIP),
		"serverIp":  serverIP.String(),
		"broadcast": bcast.String(),
	}
}

func (a *App) StopDHCPServer() {
	if a.dhcpServer == nil {
		return
	}
	a.dhcpServer.stop()
	a.dhcpServer = nil
	a.emitLog("DHCP 已停止")
}

func (a *App) GetDHCPStatus() map[string]interface{} {
	if a.dhcpServer == nil {
		return map[string]interface{}{"running": false}
	}
	s := a.dhcpServer
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{
		"running":   s.running,
		"serverIp":  s.serverIP.String(),
		"broadcast": s.bcastIP.String(),
		"pool":      s.startIP.String() + " ~ " + s.endIP.String(),
		"leases":    len(s.leases),
	}
}

func (s *DHCPServer) stop() {
	s.mu.Lock()
	s.running = false
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (s *DHCPServer) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// ─── 收发循环 ─────────────────────────────────────────────────────

func (s *DHCPServer) serve() error {
	buffer := make([]byte, 1500)
	for {
		n, addr, err := s.conn.ReadFrom(buffer)
		if err != nil {
			if s.isRunning() {
				return err
			}
			return nil
		}
		if n < 240 {
			continue
		}
		req := dhcp4.Packet(buffer[:n])
		if req.HLen() > 16 {
			continue
		}
		options := req.ParseOptions()
		t := options[dhcp4.OptionDHCPMessageType]
		if len(t) != 1 {
			continue
		}
		reqType := dhcp4.MessageType(t[0])
		if reqType < dhcp4.Discover || reqType > dhcp4.Inform {
			continue
		}

		res := s.ServeDHCP(req, reqType, options)
		if res == nil {
			continue
		}

		dst := s.replyAddr(req, addr)
		if _, err := s.conn.WriteTo(res, dst); err != nil {
			// 定向广播发不出去（例如网卡没路由）时退回有限广播再试一次
			if _, err2 := s.conn.WriteTo(res, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}); err2 != nil {
				s.emitDHCPLog(fmt.Sprintf("发送应答失败(%s): %v", dst, err))
			}
		}
	}
}

// replyAddr 决定应答发往哪里：
// 客户端还没地址（0.0.0.0）或请求了广播标志时，发给本网段定向广播，
// 否则按客户端现有地址单播。
func (s *DHCPServer) replyAddr(req dhcp4.Packet, addr net.Addr) net.Addr {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return addr
	}
	if ua.IP == nil || ua.IP.IsUnspecified() || ua.IP.Equal(net.IPv4bcast) || req.Broadcast() {
		if s.bcastIP != nil {
			return &net.UDPAddr{IP: s.bcastIP, Port: 68}
		}
		return &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	}
	return addr
}

// ─── DHCP 协议处理 ────────────────────────────────────────────────

func (s *DHCPServer) ServeDHCP(pkt dhcp4.Packet, msgType dhcp4.MessageType, options dhcp4.Options) dhcp4.Packet {
	mac := pkt.CHAddr().String()

	// DHCPRELEASE / DHCPDECLINE：回收租约
	if msgType == dhcp4.Release || msgType == dhcp4.Decline {
		s.mu.Lock()
		if l, ok := s.leases[mac]; ok {
			delete(s.leases, mac)
			delete(s.usedIPs, l.IP)
			s.emitDHCPLog(fmt.Sprintf("%s: %s 回收 %s", dhcpMsgName(msgType), mac, l.IP))
		}
		s.mu.Unlock()
		return nil
	}

	// DHCPINFORM：客户端已有地址，只需要网络参数
	if msgType == dhcp4.Inform {
		return dhcp4.ReplyPacket(pkt, dhcp4.ACK, s.serverIP.To4(), nil, 0, s.netOptions())
	}

	s.mu.Lock()
	lease, ok := s.leases[mac]
	var ip net.IP
	if ok {
		ip = net.ParseIP(lease.IP).To4()
	}
	fresh := false
	if ip == nil {
		ip = s.allocLocked()
		if ip == nil {
			s.mu.Unlock()
			s.emitDHCPLog("地址池已耗尽，无法为 " + mac + " 分配地址")
			return nil
		}
		fresh = true
		s.emitDHCPLog(fmt.Sprintf("分配: %s → %s", mac, ip))
	} else {
		s.emitDHCPLog(fmt.Sprintf("续租: %s → %s", mac, ip))
	}

	// DISCOVER 阶段只是「预留」，给 2 分钟即可：
	// 客户端拿到 Offer 却没来 Request（关机、换口、放弃）时，地址能尽快回收，
	// 不至于因为 24h 的租约把池子占满。真正下发地址的 ACK 才给完整租期。
	ttl := dhcpLeaseDuration
	if fresh && msgType == dhcp4.Discover {
		ttl = 2 * time.Minute
	}
	s.leases[mac] = DHCPLease{IP: ip.String(), MAC: mac, Expires: time.Now().Add(ttl)}
	s.usedIPs[ip.String()] = true
	s.mu.Unlock()

	// DHCPREQUEST：客户端点名的地址不是我们分配的，回 NAK 让它重新 Discover
	if msgType == dhcp4.Request {
		if reqIP := net.IP(options[dhcp4.OptionRequestedIPAddress]).To4(); reqIP != nil && !reqIP.Equal(ip) {
			s.emitDHCPLog(fmt.Sprintf("NAK: %s 请求 %s，本机分配 %s", mac, reqIP, ip))
			return dhcp4.ReplyPacket(pkt, dhcp4.NAK, s.serverIP.To4(), nil, 0, nil)
		}
	}

	mt := dhcp4.Offer
	if msgType == dhcp4.Request {
		mt = dhcp4.ACK
	}
	return dhcp4.ReplyPacket(pkt, mt, s.serverIP.To4(), ip, dhcpLeaseDuration, s.netOptions())
}

func (s *DHCPServer) netOptions() []dhcp4.Option {
	opts := []dhcp4.Option{
		{Code: dhcp4.OptionSubnetMask, Value: []byte(s.netMask)},
	}
	if s.gateway != nil {
		opts = append(opts,
			dhcp4.Option{Code: dhcp4.OptionRouter, Value: s.gateway.To4()},
			dhcp4.Option{Code: dhcp4.OptionDomainNameServer, Value: s.gateway.To4()},
		)
	}
	return opts
}

// allocLocked 在地址池内找一个未使用的地址（调用方需持锁）
func (s *DHCPServer) allocLocked() net.IP {
	now := time.Now()
	for mac, l := range s.leases {
		if now.After(l.Expires) {
			delete(s.leases, mac)
			delete(s.usedIPs, l.IP)
		}
	}

	start := binary.BigEndian.Uint32(s.startIP.To4())
	end := binary.BigEndian.Uint32(s.endIP.To4())
	poolNet := s.serverIP.Mask(s.netMask)
	for v := start; ; v++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, v)
		if !s.usedIPs[ip.String()] && !ip.Equal(s.serverIP) && !ip.Equal(s.bcastIP) && !ip.Equal(poolNet) {
			return ip
		}
		if v == end || v == 0xFFFFFFFF {
			break
		}
	}
	return nil
}

// ─── 租约查询 ─────────────────────────────────────────────────────

func (a *App) GetDHCPLeases() []map[string]interface{} {
	if a.dhcpServer == nil {
		return nil
	}
	s := a.dhcpServer
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	result := make([]map[string]interface{}, 0, len(s.leases))
	for _, l := range s.leases {
		exp := l.Expires
		expStr := exp.Format("15:04:05")
		if exp.Sub(now) > 24*time.Hour || exp.YearDay() != now.YearDay() {
			expStr = exp.Format("01-02 15:04")
		}
		result = append(result, map[string]interface{}{
			"mac":     l.MAC,
			"ip":      l.IP,
			"expires": expStr,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		a := net.ParseIP(result[i]["ip"].(string)).To4()
		b := net.ParseIP(result[j]["ip"].(string)).To4()
		if a == nil || b == nil {
			return result[i]["ip"].(string) < result[j]["ip"].(string)
		}
		return binary.BigEndian.Uint32(a) < binary.BigEndian.Uint32(b)
	})
	return result
}

// ─── 工具函数 ─────────────────────────────────────────────────────

func dhcpMsgName(t dhcp4.MessageType) string {
	switch t {
	case dhcp4.Release:
		return "释放"
	case dhcp4.Decline:
		return "拒绝"
	}
	return fmt.Sprintf("消息%d", byte(t))
}

// broadcastIP 计算 ip/mask 所在子网的定向广播地址
func broadcastIP(ip net.IP, mask net.IPMask) net.IP {
	v4 := ip.To4()
	m := mask
	if len(m) == 16 {
		m = m[12:]
	}
	if v4 == nil || len(m) != 4 {
		return net.IPv4bcast
	}
	b := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		b[i] = v4[i] | ^m[i]
	}
	return b
}

// ifaceIPv4ForSubnet 找到所选网卡的 IPv4 地址，优先返回与地址池同网段的那个。
// ifaceName 一般是 Npcap 的设备名（\Device\NPF_{GUID}），先按设备名精确匹配，
// 再用 PowerShell/netsh 友好名兜底。
func ifaceIPv4ForSubnet(ifaceName string, pool net.IP, mask net.IPMask) (net.IP, bool) {
	var fallback net.IP

	if devs, err := pcap.FindAllDevs(); err == nil {
		for _, d := range devs {
			if d.Name != ifaceName {
				continue
			}
			for _, a := range d.Addresses {
				v4 := a.IP.To4()
				if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
					continue
				}
				if v4.Mask(mask).Equal(pool.Mask(mask)) {
					return v4, true
				}
				if fallback == nil {
					fallback = v4
				}
			}
			break
		}
	}

	if name := resolveIfaceName(ifaceName); name != "" {
		if ifc, err := net.InterfaceByName(name); err == nil {
			if addrs, err := ifc.Addrs(); err == nil {
				for _, a := range addrs {
					n, ok := a.(*net.IPNet)
					if !ok {
						continue
					}
					v4 := n.IP.To4()
					if v4 == nil || v4.IsLinkLocalUnicast() {
						continue
					}
					if v4.Mask(mask).Equal(pool.Mask(mask)) {
						return v4, true
					}
					if fallback == nil {
						fallback = v4
					}
				}
			}
		}
	}

	if fallback != nil {
		return fallback, true
	}
	return nil, false
}

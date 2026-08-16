package main

import (
	"strings"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─── 被动嗅探识别增强（PR：嗅探增强）────────────────────────────────
// 零主动流量（混杂监听只收不发），从以下被动事件识别设备/BMC：
//   - ARP   ：免费 ARP / 请求应答（链路事件即时发现，如拔插网线）
//   - mDNS  ：UDP 5353 组播通告 —— BMC（iDRAC/iLO/iBMC/XCC 等）默认注册
//             _ipmi._tcp / _bmc._tcp 等服务并周期重播（TTL 75~120s），
//             常开监听即可在数分钟内自动发现，无需拔插网线
//   - DHCP  ：UDP 67/68 请求（走 DHCP 的 BMC 每次要地址都会发包）
//   - LLDP  ：0x88CC 链路层通告（直连/交换机侧信息，记录日志）
// 安全考量：不发送任何探测包，安全设备（IDS/IPS/准入）无感。

// parseSniffPacket 解析单个捕获包并尝试识别设备
func (a *App) parseSniffPacket(packet gopacket.Packet, myMAC string) {
	// ─── ARP（现有逻辑迁移）──────────────────────────────────────
	if arpLayer := packet.Layer(layers.LayerTypeARP); arpLayer != nil {
		arp := arpLayer.(*layers.ARP)
		if arp.Operation == layers.ARPReply || arp.Operation == layers.ARPRequest {
			ip := netIPToString(arp.SourceProtAddress)
			mac := netMACToString(arp.SourceHwAddress)
			if ip != "" && ip != "0.0.0.0" && mac != "" && mac != myMAC {
				vendor, isBMC := lookupOUI(mac)
				a.emitDevice(ip, mac, vendor, isBMC, "arp", "")
			}
		}
		return
	}

	// ─── LLDP（链路层通告，仅记录日志辅助判断）────────────────────
	if lldpLayer := packet.Layer(layers.LayerTypeLinkLayerDiscovery); lldpLayer != nil {
		lldp := lldpLayer.(*layers.LinkLayerDiscovery)
		var sysName, chassisID string
		for _, tlv := range lldp.Values {
			switch tlv.Type {
			case layers.LLDPTLVSysName:
				sysName = string(tlv.Value)
			case layers.LLDPTLVChassisID:
				chassisID = string(tlv.Value)
			}
		}
		if sysName != "" || chassisID != "" {
			a.emitLog("LLDP 通告: 系统=" + sysName + " 对端=" + chassisID)
		}
		return
	}

	// ─── UDP 层（mDNS / DHCP）────────────────────────────────────
	udpLayer := packet.Layer(layers.LayerTypeUDP)
	if udpLayer == nil {
		return
	}
	udp := udpLayer.(*layers.UDP)

	// mDNS：目的或源端口 5353（应答与查询都发往 5353）
	if udp.DstPort == 5353 || udp.SrcPort == 5353 {
		a.parseMDNS(packet, myMAC)
		return
	}

	// DHCP：客户端 68 / 服务器 67
	if udp.DstPort == 67 || udp.DstPort == 68 || udp.SrcPort == 67 || udp.SrcPort == 68 {
		a.parseDHCP(packet, myMAC)
	}
}

// emitDevice 统一上报设备（hostname/service 为增强字段，前端兼容忽略）
func (a *App) emitDevice(ip, mac, vendor string, isBMC bool, via, service string) {
	if a.ctx == nil {
		return
	}
	hostname := service
	if idx := strings.Index(service, "."); idx > 0 {
		hostname = service[:idx]
	}
	runtime.EventsEmit(a.ctx, "device-found", DeviceInfo{
		IP: ip, MAC: mac, Vendor: vendor, IsBMC: isBMC, Hostname: hostname,
	})
	if service != "" {
		a.emitLog("被动识别: " + ip + " " + mac + " " + vendor + " [" + via + "] " + service)
	}
}

// ─── mDNS / DNS-SD 解析 ────────────────────────────────────────────

// bmcServiceHints mDNS 服务类型与实例名中的 BMC 特征关键字
var bmcServiceHints = []string{
	"_ipmi._tcp", "_bmc._tcp", "_bmc-mgmt._tcp",
	"idrac", "ilo", "ibmc", "xcc", "bmc", "ipmi",
}

func isBMCServices(services []string) bool {
	for _, s := range services {
		lower := strings.ToLower(s)
		for _, hint := range bmcServiceHints {
			if strings.Contains(lower, hint) {
				return true
			}
		}
	}
	return false
}

// parseMDNS 解析 mDNS 应答：提取 A 记录 IP、PTR 服务实例名（含型号）
func (a *App) parseMDNS(packet gopacket.Packet, myMAC string) {
	dnsLayer := packet.Layer(layers.LayerTypeDNS)
	if dnsLayer == nil {
		return
	}
	dns := dnsLayer.(*layers.DNS)
	if !dns.QR {
		return // 只看应答/通告
	}

	var ips []string
	var services []string
	for _, ans := range dns.Answers {
		switch ans.Type {
		case layers.DNSTypeA:
			if ans.IP != nil && ans.IP.To4() != nil {
				ips = append(ips, ans.IP.String())
			}
		case layers.DNSTypePTR:
			if len(ans.PTR) > 0 {
				services = append(services, string(ans.PTR))
			}
		}
	}
	if len(services) == 0 && len(ips) == 0 {
		return
	}

	// 源 MAC（以太网层）
	ethLayer := packet.Layer(layers.LayerTypeEthernet)
	if ethLayer == nil {
		return
	}
	mac := ethLayer.(*layers.Ethernet).SrcMAC.String()
	if mac == "" || mac == myMAC {
		return
	}
	// IP 优先取 A 记录；兜底取 IPv4 源地址
	ip := ""
	if len(ips) > 0 {
		ip = ips[0]
	} else if ipv4 := packet.Layer(layers.LayerTypeIPv4); ipv4 != nil {
		ip = ipv4.(*layers.IPv4).SrcIP.String()
	}
	if ip == "" || ip == "0.0.0.0" {
		return
	}

	vendor, ouiBMC := lookupOUI(mac)
	svcBMC := isBMCServices(services)
	service := strings.Join(services, ",")
	a.emitDevice(ip, mac, vendor, ouiBMC || svcBMC, "mdns", service)
}

// ─── DHCP 解析 ─────────────────────────────────────────────────────

// parseDHCP 从 DHCP 报文提取 MAC + 请求/分配 IP + 主机名
func (a *App) parseDHCP(packet gopacket.Packet, myMAC string) {
	dhcpLayer := packet.Layer(layers.LayerTypeDHCPv4)
	if dhcpLayer == nil {
		return
	}
	dhcp := dhcpLayer.(*layers.DHCPv4)
	mac := dhcp.ClientHWAddr.String()
	if mac == "" || mac == myMAC {
		return
	}

	// IP 优先级：YourIP > Option 50（Requested IP）
	ip := dhcp.YourClientIP.String()
	if ip == "" || ip == "0.0.0.0" {
		for _, opt := range dhcp.Options {
			if opt.Type == layers.DHCPOptRequestIP && len(opt.Data) == 4 {
				ip = netIPToString(opt.Data)
				break
			}
		}
	}
	if ip == "" || ip == "0.0.0.0" {
		return // 无 IP 的报文（Discover 无请求地址）不产生事件
	}

	hostname := ""
	for _, opt := range dhcp.Options {
		if opt.Type == layers.DHCPOptHostname {
			hostname = string(opt.Data)
			break
		}
	}

	vendor, ouiBMC := lookupOUI(mac)
	isBMC := ouiBMC
	// DHCP 无法直接判定 BMC：主机名含特征关键字时提升为 BMC
	hn := strings.ToLower(hostname)
	for _, hint := range bmcServiceHints {
		if strings.Contains(hn, hint) {
			isBMC = true
			break
		}
	}
	service := ""
	if hostname != "" {
		service = "hostname=" + hostname
	}
	a.emitDevice(ip, mac, vendor, isBMC, "dhcp", service)
}

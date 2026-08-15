package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─── MAC OUI 数据库 ────────────────────────────────────────────────

var macOuiDB = map[string]string{
	// 浪潮 Inspur
	"00:0E:1E": "浪潮 Inspur", "00:1E:7E": "浪潮 Inspur", "00:22:92": "浪潮 Inspur",
	"00:23:7A": "浪潮 Inspur", "00:24:AC": "浪潮 Inspur", "00:25:28": "浪潮 Inspur",
	"00:26:4D": "浪潮 Inspur", "00:30:AB": "浪潮 Inspur", "04:1F:AA": "浪潮 Inspur",
	"04:7D:7B": "浪潮 Inspur", "04:8B:42": "浪潮 Inspur", "04:C0:5B": "浪潮 Inspur",
	"08:62:66": "浪潮 Inspur", "0C:12:62": "浪潮 Inspur", "0C:47:3D": "浪潮 Inspur",
	"0C:51:01": "浪潮 Inspur", "0C:72:D9": "浪潮 Inspur", "0C:96:CD": "浪潮 Inspur",
	"0C:C4:13": "浪潮 Inspur", "10:3B:59": "浪潮 Inspur", "10:62:C9": "浪潮 Inspur",
	"10:7B:EF": "浪潮 Inspur", "14:1A:A3": "浪潮 Inspur", "14:75:90": "浪潮 Inspur",
	"18:0C:77": "浪潮 Inspur", "18:66:DA": "浪潮 Inspur", "18:8B:45": "浪潮 Inspur",
	"18:D6:C7": "浪潮 Inspur", "1C:3E:84": "浪潮 Inspur", "1C:4A:F7": "浪潮 Inspur",
	"1C:5A:6B": "浪潮 Inspur", "1C:7B:21": "浪潮 Inspur", "1C:AB:A7": "浪潮 Inspur",
	"1C:DD:EA": "浪潮 Inspur", "20:1A:5C": "浪潮 Inspur", "20:6A:8A": "浪潮 Inspur",
	"20:D5:BF": "浪潮 Inspur", "20:E8:82": "浪潮 Inspur", "24:2E:02": "浪潮 Inspur",
	"24:77:03": "浪潮 Inspur", "24:9E:AB": "浪潮 Inspur", "24:AA:4C": "浪潮 Inspur",
	"24:BE:05": "浪潮 Inspur", "24:D0:DF": "浪潮 Inspur", "24:F0:FF": "浪潮 Inspur",
	"28:11:7E": "浪潮 Inspur", "28:24:FF": "浪潮 Inspur", "28:87:BA": "浪潮 Inspur",
	"28:D0:44": "浪潮 Inspur", "28:ED:E0": "浪潮 Inspur", "28:EF:01": "浪潮 Inspur",
	"2C:10:C1": "浪潮 Inspur", "2C:26:C5": "浪潮 Inspur", "2C:2D:48": "浪潮 Inspur",
	"2C:E0:DC": "浪潮 Inspur", "38:A2:8C": "浪潮 Inspur", "48:4D:46": "浪潮 Inspur",
	"48:5A:3F": "浪潮 Inspur", "4C:52:62": "浪潮 Inspur", "6C:92:BF": "浪潮 Inspur",
	"80:6E:DA": "浪潮 Inspur", "98:EE:CB": "浪潮 Inspur", "9C:EB:E8": "浪潮 Inspur",
	"A0:36:BC": "浪潮 Inspur", "A4:C3:F0": "浪潮 Inspur", "D0:67:E5": "浪潮 Inspur",
	"D8:1C:79": "浪潮 Inspur", "FC:5A:EE": "浪潮 Inspur", "50:79:73": "浪潮 Inspur",
	// 华为
	"00:18:82": "华为 Huawei", "00:25:9E": "华为 Huawei", "28:6E:D4": "华为 Huawei",
	"48:DF:37": "华为 Huawei", "00:E0:FC": "华为 Huawei", "D8:49:0B": "华为 Huawei",
	"04:5F:A7": "华为 Huawei", "10:47:80": "华为 Huawei", "20:0B:C7": "华为 Huawei",
	"24:8A:A6": "华为 Huawei", "30:1A:1D": "华为 Huawei", "34:96:72": "华为 Huawei",
	"44:67:49": "华为 Huawei", "4C:1A:95": "华为 Huawei", "60:DE:44": "华为 Huawei",
	"78:D7:52": "华为 Huawei", "84:A8:D8": "华为 Huawei", "88:25:93": "华为 Huawei",
	"A0:99:9B": "华为 Huawei", "AC:85:3D": "华为 Huawei", "B0:C5:54": "华为 Huawei",
	"C8:38:70": "华为 Huawei", "DC:BF:E4": "华为 Huawei", "EC:1D:7F": "华为 Huawei",
	// Dell
	"00:06:5B": "Dell", "00:14:22": "Dell", "00:15:C5": "Dell", "00:1C:23": "Dell",
	"00:23:AE": "Dell", "14:18:77": "Dell", "18:DB:F2": "Dell", "24:6E:96": "Dell",
	"34:17:EB": "Dell", "B8:CA:3A": "Dell", "D0:94:66": "Dell", "D4:BE:D9": "Dell",
	"F0:4D:A2": "Dell", "F8:BC:12": "Dell", "1C:40:24": "Dell iDRAC",
	"28:F1:0E": "Dell", "50:9A:4C": "Dell", "74:86:7A": "Dell",
	"78:45:C4": "Dell", "80:18:44": "Dell", "84:8F:69": "Dell",
	"90:B1:1C": "Dell", "98:90:96": "Dell", "A0:1D:48": "Dell",
	"A4:4C:C8": "Dell", "B8:2A:72": "Dell", "C8:1F:66": "Dell iDRAC",
	"D0:43:1E": "Dell", "F4:8E:38": "Dell", "F8:CA:B8": "Dell",
	// HPE
	"00:10:18": "HPE", "00:17:A4": "HPE", "00:1B:78": "HPE", "00:1C:C4": "HPE",
	"2C:76:8A": "HPE iLO", "3C:D9:2B": "HPE", "94:18:82": "HPE", "AC:16:2D": "HPE",
	"28:80:23": "HPE iLO", "30:8D:99": "HPE", "38:63:BB": "HPE",
	"50:65:F3": "HPE", "5C:8A:38": "HPE", "60:57:18": "HPE", "68:B5:99": "HPE",
	"78:E7:D1": "HPE", "80:C1:6E": "HPE", "9C:8E:99": "HPE", "D8:9D:67": "HPE",
	// Supermicro
	"00:30:48": "Supermicro", "0C:C4:7A": "Supermicro", "3C:EC:EF": "Supermicro",
	"AC:1F:6B": "Supermicro", "18:C0:4D": "Supermicro", "20:47:47": "Supermicro",
	"34:02:86": "Supermicro", "38:CA:73": "Supermicro", "40:62:31": "Supermicro",
	"48:EA:63": "Supermicro", "58:10:8C": "Supermicro", "60:A4:4C": "Supermicro",
	"6C:2E:85": "Supermicro", "78:E3:B5": "Supermicro", "8C:89:A5": "Supermicro",
	"98:03:CF": "Supermicro", "B0:6E:BF": "Supermicro", "B4:2C:BE": "Supermicro",
	// Lenovo
	"8C:F6:81": "联想 Lenovo", "00:23:7D": "联想 Lenovo", "08:9E:01": "联想 Lenovo",
	"00:1A:6C": "联想 Lenovo", "3C:97:0E": "联想 Lenovo", "38:68:DD": "联想 Lenovo",
	"54:EE:75": "联想 Lenovo", "68:F7:28": "联想 Lenovo", "70:F3:95": "联想 Lenovo",
	"B0:83:FE": "联想 Lenovo", "C8:5B:76": "联想 Lenovo", "E8:6A:64": "联想 Lenovo",
	// Cisco UCS
	"00:1E:49": "Cisco UCS", "00:3A:99": "Cisco UCS", "00:1A:A2": "Cisco UCS",
	"00:25:B5": "Cisco UCS", "00:26:51": "Cisco UCS",
	// Fujitsu / Oracle / Others
	"00:1A:A0": "富士通 Fujitsu", "00:19:99": "富士通 Fujitsu",
	"00:14:4F": "Oracle (原 Sun)", "00:E0:81": "Oracle (原 Sun)",
	"00:0C:29": "VMware ESXi", "00:50:56": "VMware ESXi",
	"00:25:22": "ASRock Rack", "D0:50:99": "ASRock Rack", "70:85:C2": "ASRock Rack",
	"00:1F:D0": "Gigabyte", "B4:2E:99": "Gigabyte", "FC:AA:14": "Gigabyte",
	// Intel
	"00:1B:21": "Intel", "00:1E:64": "Intel", "A4:5D:36": "Intel",
	"1C:69:7A": "Intel", "40:16:7E": "Intel BMC", "4C:34:88": "Intel",
	// Quanta / Wistron / Inventec
	"00:30:D6": "Quanta QCT", "00:14:2C": "Quanta QCT", "08:9C:86": "Quanta QCT",
	"00:13:CE": "Wistron 纬创", "00:13:9F": "Inventec 英业达",
	// ZTE / Sugon
	"00:19:C6": "中兴 ZTE", "00:1E:73": "中兴 ZTE", "10:89:FB": "中兴 ZTE",
	"00:15:5D": "曙光 Sugon", "00:1D:0F": "曙光 Sugon",
	// Advantech / IEI
	"00:14:7C": "研华 Advantech", "00:0E:5B": "威强 IEI",
}

var bmcHighlights = map[string]bool{
	"Inspur": true, "浪潮": true, "IEISYSTEM": true, "Huawei": true, "华为": true,
	"Dell": true, "HPE": true, "Supermicro": true, "Lenovo": true, "联想": true,
	"ASRock Rack": true, "Gigabyte": true, "Cisco UCS": true, "Oracle": true, "Sun": true,
	"Fujitsu": true, "富士通": true, "Quanta": true, "QCT": true, "Wistron": true,
	"纬创": true, "Inventec": true, "英业达": true, "Sugon": true, "曙光": true,
	"ZTE": true, "中兴": true, "Intel": true, "NEC": true, "VMware": true,
}

// ─── 数据结构 ──────────────────────────────────────────────────────

type DeviceInfo struct {
	IP      string `json:"ip"`
	MAC     string `json:"mac"`
	Vendor  string `json:"vendor"`
	IsBMC   bool   `json:"isBmc"`
	Hostname string `json:"hostname"`
}

type InterfaceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	NetshName   string `json:"netshName"`
	IP          string `json:"ip"`
	Subnet      string `json:"subnet"`
	HasIP       bool   `json:"hasIp"`
}

// ─── App ───────────────────────────────────────────────────────────

type App struct {
	ctx               context.Context
	stopCh            chan struct{}
	dhcpServer        *DHCPServer
}

func NewApp() *App {
	return &App{stopCh: make(chan struct{})}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.ensureDataDir()
	log.Println("[OOBKit] 启动完成")
	refreshAdapCache()
	if len(adapCache) > 0 {
		a.emitLog(fmt.Sprintf("网卡映射就绪: %d 条", len(adapCache)))
		for i, ad := range adapCache {
			a.emitLog(fmt.Sprintf("  [%d] %s | GUID=%s", i, ad.ConnName, ad.GUID))
		}
	} else {
		a.emitLog("警告: 网卡映射为空，配网功能可能不可用")
	}
}

func (a *App) emitLog(msg string) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "log", time.Now().Format("15:04:05")+" "+msg)
	}
}

func (a *App) emitDHCPLog(msg string) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "dhcp-log", time.Now().Format("15:04:05")+" "+msg)
	}
}

func (a *App) OpenFileDialog() map[string]interface{} {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 FRU 文件",
		Filters: []runtime.FileFilter{
			{DisplayName: "FRU 文件 (*.bin)", Pattern: "*.bin"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil || path == "" {
		return map[string]interface{}{"path": ""}
	}
	return map[string]interface{}{"path": path}
}

func (a *App) shutdown(ctx context.Context) {
	close(a.stopCh)
}

// ─── OUI 查询 ─────────────────────────────────────────────────────

func (a *App) LookupOUI(mac string) map[string]interface{} {
	mac = strings.ToUpper(strings.TrimSpace(mac))
	prefix := ""
	if len(mac) >= 8 {
		prefix = mac[:8]
	}
	vendor := macOuiDB[prefix]
	if vendor == "" {
		vendor = "Unknown"
	}
	isBMC := false
	for kw := range bmcHighlights {
		if strings.Contains(strings.ToLower(vendor), strings.ToLower(kw)) {
			isBMC = true
			break
		}
	}
	return map[string]interface{}{
		"vendor": vendor,
		"isBmc":  isBMC,
	}
}

// ─── 获取网卡列表 ─────────────────────────────────────────────────

func (a *App) GetInterfaces() []InterfaceInfo {
	if len(adapCache) == 0 {
		refreshAdapCache()
	}

	devs, err := pcap.FindAllDevs()
	if err != nil {
		log.Printf("pcap.FindAllDevs 失败: %v", err)
		a.emitLog("获取网卡列表失败: " + err.Error())
		return nil
	}

	ipconfigMap := getWindowsIPConfig()
	seen := make(map[string]bool)

	var ifaces []InterfaceInfo
	for _, dev := range devs {
		name := dev.Name
		netshName := getNetshName(name)
		desc := netshName
		if desc == "" {
			if cd, ok := ipconfigMap[getIfaceMACStr(name)]; ok {
				desc = cd
			}
		}
		if desc == "" {
			desc = dev.Description
		}
		if desc == "" {
			desc = name
		}

		// 跳过虚拟网卡和蓝牙
		lower := strings.ToLower(desc)
		if strings.Contains(lower, "bluetooth") ||
			strings.Contains(lower, "蓝牙") ||
			strings.Contains(lower, "hyper-v") ||
			strings.Contains(lower, "wsl") ||
			strings.Contains(lower, "vethernet") {
			continue
		}

		// 用 GUID 去重
		guid := ""
		for _, ad := range adapCache {
			if ad.ConnName == netshName || ad.ConnName == desc {
				guid = ad.GUID
				break
			}
		}
		if guid == "" {
			guid = desc
		}
		if seen[guid] {
			continue
		}
		seen[guid] = true

		ip := ""
		subnet := ""
		for _, addr := range dev.Addresses {
			if addr.IP.To4() != nil && !addr.IP.IsLoopback() {
				ip = addr.IP.String()
				ones, _ := addr.Netmask.Size()
				subnet = fmt.Sprintf("%s/%d", addr.IP.Mask(addr.Netmask).String(), ones)
				break
			}
		}
		ifaces = append(ifaces, InterfaceInfo{
			Name: name, Description: desc, NetshName: netshName, IP: ip,
			Subnet: subnet, HasIP: ip != "",
		})
	}
	return ifaces
}

func getWindowsIPConfig() map[string]string {
	out, err := execCmdHidden("ipconfig", "/all")
	if err != nil {
		return nil
	}
	m := make(map[string]string)
	currentMAC := ""
	currentDesc := ""
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		// 检测网卡段：提取干净的名称
		// 英文: "Ethernet adapter Ethernet0:"  → "Ethernet0"
		// 中文: "以太网适配器 以太网:"         → "以太网"
		if strings.Contains(strings.ToLower(trimmed), "adapter") ||
			strings.Contains(trimmed, "适配器") {
			currentMAC = ""
			// 去掉尾部冒号
			s := strings.TrimRight(trimmed, ":")
			// 拆出 adapter/适配器 后面的名称
			idx := strings.LastIndex(s, " ")
			if idx > 0 {
				s = strings.TrimSpace(s[idx+1:])
				// 去掉可能的 "adapter" 残余
				s = strings.TrimPrefix(s, "adapter ")
				s = strings.TrimPrefix(s, "适配器 ")
			}
			currentDesc = s
		}
		// MAC
		if strings.Contains(trimmed, "Physical Address") ||
			strings.Contains(trimmed, "物理地址") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				currentMAC = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(parts[1]), "-", ":"))
			}
		}
		if currentMAC != "" && currentDesc != "" {
			m[currentMAC] = currentDesc
		}
	}
	return m
}

// ─── ARP 主动扫描 ─────────────────────────────────────────────────

func (a *App) StartScan(ifaceName string, subnet string) {
	a.emitLog("开始扫描 " + subnet + " ...")
	myMAC := getIfaceMACStr(ifaceName)
	go func() {
		defer func() {
			runtime.EventsEmit(a.ctx, "scan-complete")
			a.emitLog("扫描完成")
		}()

		handle, err := pcap.OpenLive(ifaceName, 65536, true, pcap.BlockForever)
		if err != nil {
			log.Printf("打开网卡失败: %v", err)
			runtime.EventsEmit(a.ctx, "scan-error", "打开网卡失败: "+err.Error())
			return
		}
		defer handle.Close()

		// 构造 ARP 请求（逐个 IP）
		ips := expandSubnet(subnet)
		for i, ip := range ips {
			select {
			case <-a.stopCh:
				return
			default:
			}
			mac := arpPing(handle, ifaceName, ip)
			if mac != "" && mac != myMAC {
				vendor, isBMC := lookupOUI(mac)
				dev := DeviceInfo{IP: ip, MAC: mac, Vendor: vendor, IsBMC: isBMC}
				runtime.EventsEmit(a.ctx, "device-found", dev)
				if isBMC {
					a.emitLog("发现 BMC: " + ip + " | " + mac + " | " + vendor)
				}
			}
			// 进度
			if i%50 == 0 || i == len(ips)-1 {
				runtime.EventsEmit(a.ctx, "scan-progress", map[string]int{
					"current": i + 1, "total": len(ips),
				})
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
}

func arpPing(handle *pcap.Handle, iface string, targetIP string) string {
	eth := &layers.Ethernet{
		SrcMAC:       getIfaceMAC(iface),
		DstMAC:       []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeARP,
	}
	arp := &layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   getIfaceMAC(iface),
		SourceProtAddress: getIfaceIP(iface),
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
		DstProtAddress:    parseIPv4(targetIP),
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	gopacket.SerializeLayers(buf, opts, eth, arp)

	handle.WritePacketData(buf.Bytes())

	// 等待响应
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case <-timeout:
			return ""
		case packet := <-packetSource.Packets():
			if arpLayer := packet.Layer(layers.LayerTypeARP); arpLayer != nil {
				arpResp := arpLayer.(*layers.ARP)
				if arpResp.Operation == layers.ARPReply &&
					netIPToString(arpResp.SourceProtAddress) == targetIP {
					return netMACToString(arpResp.SourceHwAddress)
				}
			}
		}
	}
}

// ─── 被动监听 ─────────────────────────────────────────────────────

func (a *App) StartSniff(ifaceName string) {
	a.emitLog("开始被动监听 " + ifaceName + " ...")
	myMAC := getIfaceMACStr(ifaceName) // 本机 MAC，用于过滤
	go func() {
		handle, err := pcap.OpenLive(ifaceName, 65536, true, pcap.BlockForever)
		if err != nil {
			runtime.EventsEmit(a.ctx, "sniff-error", "打开网卡失败: "+err.Error())
			a.emitLog("监听失败: " + err.Error())
			return
		}
		defer handle.Close()

		packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
		for {
			select {
			case <-a.stopCh:
				runtime.EventsEmit(a.ctx, "sniff-complete")
				a.emitLog("监听已停止")
				return
			case packet := <-packetSource.Packets():
				if arpLayer := packet.Layer(layers.LayerTypeARP); arpLayer != nil {
					arp := arpLayer.(*layers.ARP)
					if arp.Operation == layers.ARPReply || arp.Operation == layers.ARPRequest {
						ip := netIPToString(arp.SourceProtAddress)
						mac := netMACToString(arp.SourceHwAddress)
						if ip != "" && ip != "0.0.0.0" && mac != "" && mac != myMAC {
							vendor, isBMC := lookupOUI(mac)
							runtime.EventsEmit(a.ctx, "device-found", DeviceInfo{
								IP: ip, MAC: mac, Vendor: vendor, IsBMC: isBMC,
							})
						}
					}
				}
			}
		}
	}()
}

func (a *App) StopAll() {
	select {
	case a.stopCh <- struct{}{}:
	default:
	}
	// 重建 channel
	a.stopCh = make(chan struct{})
}

// ─── Npcap 检测与安装 ─────────────────────────────────────────────

func (a *App) CheckNpcap() map[string]interface{} {
	paths := []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32", "Npcap", "wpcap.dll"),
		filepath.Join(os.Getenv("SystemRoot"), "SysWOW64", "Npcap", "wpcap.dll"),
	}
	if os.Getenv("SystemRoot") == "" {
		paths = []string{
			"C:\\Windows\\System32\\Npcap\\wpcap.dll",
			"C:\\Windows\\SysWOW64\\Npcap\\wpcap.dll",
		}
	}
	installed := false
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			installed = true
			break
		}
	}
	if installed {
		a.emitLog("Npcap 已检测到")
	} else {
		a.emitLog("未检测到 Npcap，需引导用户安装")
	}
	return map[string]interface{}{
		"installed": installed,
	}
}

// OpenNpcapWebsite 打开 Npcap 官网，由用户自行下载安装
func (a *App) OpenNpcapWebsite() map[string]interface{} {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", "https://npcap.com/")
	hideCmdWindow(cmd)
	if err := cmd.Start(); err != nil {
		a.emitLog("打开 Npcap 官网失败: " + err.Error())
		return map[string]interface{}{"success": false, "message": err.Error()}
	}
	a.emitLog("已打开 Npcap 官网，请用户自行下载安装")
	return map[string]interface{}{"success": true, "message": "已打开官网"}
}

// ─── IP 配置 ──────────────────────────────────────────────────────

func (a *App) SetStaticIP(iface, ip, mask, gateway string) map[string]interface{} {
	// 将 pcap 设备名翻译为 netsh 可用的友好名称
	guid := extractGUIDFromPcapName(iface)
	netshName := getNetshName(iface)
	if netshName == "" {
		a.emitLog(fmt.Sprintf("配网失败: 查询GUID=%s -> 未找到", guid))
		// 列出缓存中的 GUID 方便对比
		for i, ad := range adapCache {
			a.emitLog(fmt.Sprintf("  缓存[%d] GUID=%s 名=%s", i, ad.GUID, ad.ConnName))
		}
		return map[string]interface{}{"success": false, "message": "无法找到网卡的系统名称，请确认网卡已启用"}
	}
	args := []string{"interface", "ip", "set", "address",
		fmt.Sprintf("name=%s", netshName), "source=static",
		fmt.Sprintf("addr=%s", ip), fmt.Sprintf("mask=%s", mask)}
	if gateway != "" {
		args = append(args, fmt.Sprintf("gateway=%s", gateway))
	} else {
		args = append(args, "gateway=none")
	}
	a.emitLog(fmt.Sprintf("netsh %s", strings.Join(args, " ")))
	cmd := exec.Command("netsh", args...)
	hideCmdWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		a.emitLog("配网失败: " + string(out))
		return map[string]interface{}{"success": false, "message": string(out)}
	}
	a.emitLog("配网成功: " + netshName + " → " + ip)
	return map[string]interface{}{"success": true, "message": "配置成功"}
}

func (a *App) SetDHCP(iface string) map[string]interface{} {
	netshName := getNetshName(iface)
	if netshName == "" {
		a.emitLog("恢复DHCP失败: 无法找到网卡系统名称")
		return map[string]interface{}{"success": false, "message": "无法找到网卡的系统名称"}
	}

	// 删除静态 IP 配置 + 启用 DHCP（和 Windows GUI 行为一致）
	psScript := fmt.Sprintf(
		"Remove-NetIPAddress -InterfaceAlias '%s' -AddressFamily IPv4 -Confirm:$false -ErrorAction SilentlyContinue; "+
			"Set-NetIPInterface -InterfaceAlias '%s' -Dhcp Enabled",
		netshName, netshName)
	a.emitLog("恢复DHCP: " + netshName)
	encoded := encodePowerShellCmd(psScript)
	out, err := execPowerShellHidden(encoded)
	if err != nil {
		a.emitLog("恢复DHCP失败: " + string(out))
		return map[string]interface{}{"success": false, "message": string(out)}
	}
	a.emitLog("已恢复DHCP: " + netshName)
	return map[string]interface{}{"success": true, "message": "已恢复 DHCP"}
}

// ─── 工具函数 ─────────────────────────────────────────────────────

func lookupOUI(mac string) (string, bool) {
	mac = strings.ToUpper(strings.TrimSpace(mac))
	prefix := ""
	if len(mac) >= 8 {
		prefix = mac[:8]
	}
	vendor := macOuiDB[prefix]
	if vendor == "" {
		vendor = "Unknown"
	}
	for kw := range bmcHighlights {
		if strings.Contains(strings.ToLower(vendor), strings.ToLower(kw)) {
			return vendor, true
		}
	}
	return vendor, false
}

func expandSubnet(subnet string) []string {
	// 简化版：假设 /24 子网
	parts := strings.Split(subnet, "/")
	if len(parts) != 2 {
		return nil
	}
	base := strings.TrimSuffix(parts[0], ".0")
	var ips []string
	for i := 1; i <= 254; i++ {
		ips = append(ips, fmt.Sprintf("%s.%d", base, i))
	}
	return ips
}

// ─── Windows 网卡名称映射 ────────────────────────────────────────
// 优先用 PowerShell Get-NetAdapter（Win8+），回退 getmac

type adapInfo struct {
	ConnName string // netsh 友好名，如 "Ethernet0"
	MAC      string
	GUID     string // 如 {A87C4374-...}
}

var adapCache []adapInfo

func refreshAdapCache() {
	adapCache = nil

	// PowerShell Get-NetAdapter — 用 Base64 编码避免引号和编码问题
	psScript := "$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding = [Text.Encoding]::UTF8; Get-NetAdapter | ForEach-Object { $_.Name + '|' + $_.MacAddress + '|' + $_.InterfaceGuid }"
	encoded := encodePowerShellCmd(psScript)
	cmd := exec.Command("powershell", "-NoProfile", "-EncodedCommand", encoded)
	hideCmdWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err == nil && len(out) > 0 {
		log.Println("[OOBKit] 使用 PowerShell 获取网卡映射")
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// 过滤 PowerShell 进度/XML 垃圾输出
			if strings.Contains(line, "<") || strings.Contains(line, "xmlns") {
				continue
			}
			parts := strings.SplitN(line, "|", 3)
			if len(parts) >= 3 {
				mac := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(parts[1]), "-", ":"))
				guid := strings.TrimSpace(parts[2])
				guid = strings.TrimPrefix(guid, "{")
				guid = strings.TrimSuffix(guid, "}")
				guid = "{" + guid + "}"
				adapCache = append(adapCache, adapInfo{
					ConnName: strings.TrimSpace(parts[0]),
					MAC:      mac,
					GUID:     guid,
				})
			}
		}
	}

	// 回退：getmac
	if len(adapCache) == 0 {
		log.Println("[OOBKit] PowerShell 未获取到，尝试 getmac")
		cmd2 := exec.Command("cmd", "/c", "chcp 65001 >nul & getmac /v /fo csv")
		hideCmdWindow(cmd2)
		out2, err2 := cmd2.CombinedOutput()
		if err2 == nil && len(out2) > 0 {
			for _, line := range strings.Split(string(out2), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "\"Connection") || strings.HasPrefix(line, "\"连接名") {
					continue
				}
				fields := splitCSVLine(line)
				if len(fields) < 4 {
					continue
				}
				connName := fields[0]
				macRaw := fields[2]
				transport := fields[3]
				mac := strings.ToUpper(strings.ReplaceAll(macRaw, "-", ":"))
				guid := ""
				if start := strings.Index(transport, "{"); start >= 0 {
					if end := strings.Index(transport, "}"); end > start {
						guid = transport[start : end+1]
					}
				}
				if connName != "" {
					adapCache = append(adapCache, adapInfo{ConnName: connName, MAC: mac, GUID: guid})
				}
			}
		}
	}

	// 兜底：net.Interfaces
	if len(adapCache) == 0 {
		log.Println("[OOBKit] 均未获取到，使用 net.Interfaces 兜底")
		nets, _ := net.Interfaces()
		for _, n := range nets {
			if len(n.HardwareAddr) == 6 {
				adapCache = append(adapCache, adapInfo{
					ConnName: n.Name,
					MAC:      strings.ToUpper(n.HardwareAddr.String()),
					GUID:     "",
				})
			}
		}
	}

	log.Printf("[OOBKit] 网卡映射: %d 条", len(adapCache))
	for _, a := range adapCache {
		log.Printf("  %s | %s | %s", a.ConnName, a.MAC, a.GUID)
	}
}

// encodePowerShellCmd 将字符串编码为 UTF-16LE Base64，供 -EncodedCommand 使用
func encodePowerShellCmd(s string) string {
	u16 := utf16.Encode([]rune(s))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func splitCSVLine(line string) []string {
	var fields []string
	inQuotes := false
	current := ""
	for _, ch := range line {
		if ch == '"' {
			inQuotes = !inQuotes
		} else if ch == ',' && !inQuotes {
			fields = append(fields, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	fields = append(fields, current)
	return fields
}

func findAdapByGUID(guid string) *adapInfo {
	for i := range adapCache {
		if guid != "" && strings.EqualFold(adapCache[i].GUID, guid) {
			return &adapCache[i]
		}
	}
	return nil
}

func findAdapByMAC(mac string) *adapInfo {
	for i := range adapCache {
		if mac != "" && strings.EqualFold(adapCache[i].MAC, mac) {
			return &adapCache[i]
		}
	}
	return nil
}

func extractGUIDFromPcapName(pcapName string) string {
	start := strings.Index(pcapName, "{")
	end := strings.Index(pcapName, "}")
	if start >= 0 && end > start {
		return pcapName[start : end+1]
	}
	return ""
}

func getIfaceMACStr(pcapName string) string {
	// 从 getmac 缓存找
	guid := extractGUIDFromPcapName(pcapName)
	if a := findAdapByGUID(guid); a != nil && a.MAC != "" {
		return a.MAC
	}
	// 回退 net.Interfaces
	nets, _ := net.Interfaces()
	for _, n := range nets {
		if strings.Contains(pcapName, n.Name) && len(n.HardwareAddr) == 6 {
			return strings.ToUpper(n.HardwareAddr.String())
		}
	}
	return ""
}

func getIfaceMAC(pcapName string) []byte {
	s := getIfaceMACStr(pcapName)
	if s == "" {
		return []byte{0, 0, 0, 0, 0, 0}
	}
	parts := strings.Split(s, ":")
	raw := make([]byte, 6)
	for i, p := range parts {
		if i >= 6 {
			break
		}
		var v uint64
		fmt.Sscanf(p, "%x", &v)
		raw[i] = byte(v)
	}
	return raw
}

func getNetshName(pcapName string) string {
	// 优先从 getmac 缓存取（GUID 精确匹配）
	guid := extractGUIDFromPcapName(pcapName)
	if a := findAdapByGUID(guid); a != nil {
		return a.ConnName
	}
	// 回退：用 MAC 匹配
	mac := getIfaceMACStr(pcapName)
	if a := findAdapByMAC(mac); a != nil {
		return a.ConnName
	}
	// 再回退：net.Interfaces 名称包含匹配
	nets, _ := net.Interfaces()
	for _, n := range nets {
		if strings.Contains(pcapName, n.Name) && len(n.HardwareAddr) == 6 {
			return n.Name
		}
	}
	return ""
}

func getIfaceIP(iface string) []byte {
	devs, _ := pcap.FindAllDevs()
	for _, d := range devs {
		if d.Name == iface {
			for _, addr := range d.Addresses {
				if addr.IP.To4() != nil {
					return addr.IP.To4()
				}
			}
		}
	}
	return []byte{0, 0, 0, 0}
}

func parseIPv4(ip string) []byte {
	parts := strings.Split(ip, ".")
	raw := make([]byte, 4)
	for i, p := range parts {
		var v int
		fmt.Sscanf(p, "%d", &v)
		raw[i] = byte(v)
	}
	return raw
}

func netIPToString(ip []byte) string {
	if len(ip) < 4 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3])
}

func netMACToString(mac []byte) string {
	if len(mac) < 6 {
		return ""
	}
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

func (a *App) addressesMAC() []byte { return nil }

// execCmdHidden 执行命令并返回输出，Windows 上隐藏窗口
func execCmdHidden(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	hideCmdWindow(cmd)
	return cmd.CombinedOutput()
}

// execPowerShellHidden 执行 PowerShell 编码命令并返回输出，隐藏窗口
func execPowerShellHidden(encoded string) ([]byte, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-EncodedCommand", encoded)
	hideCmdWindow(cmd)
	return cmd.CombinedOutput()
}

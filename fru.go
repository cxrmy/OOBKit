package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ipmi "github.com/bougou/go-ipmi"
)

var dataDir string

func (a *App) ensureDataDir() {
	home, err := os.UserHomeDir()
	if err != nil {
		a.emitLog("获取用户目录失败: " + err.Error())
		home = "."
	}
	dataDir = filepath.Join(home, "Documents", "OOBKit")
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(filepath.Join(dataDir, "fru"), 0755)
	a.emitLog("数据目录: " + dataDir)
}

// ─── FRU ──────────────────────────────────────────────────────────

var lastFRUData []byte // 最近一次导出的原始 FRU 二进制

func (a *App) ExportFRU(host, user, pass string) map[string]interface{} {
	c, err := ipmi.NewClient(host, 623, user, pass)
	if err != nil {
		return map[string]interface{}{"success": false, "message": "连接失败: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		return map[string]interface{}{"success": false, "message": "IPMI 连接失败: " + err.Error()}
	}
	defer c.Close(ctx)

	data, err := readFRUArea(ctx, c, 0)
	if err != nil {
		return map[string]interface{}{"success": false, "message": err.Error()}
	}

	lastFRUData = data
	fields := parseFRUBytes(data)

	// 保存到临时文件供导入使用
	tmpFile := filepath.Join(dataDir, "fru", strings.ReplaceAll(host, ".", "_")+".bin")
	os.WriteFile(tmpFile, data, 0644)
	fields["_file"] = tmpFile

	a.emitLog(fmt.Sprintf("FRU 导出: %s (%d bytes)", host, len(data)))
	return map[string]interface{}{"success": true, "fields": fields}
}

func (a *App) SaveFRUFile(serial string) map[string]interface{} {
	if len(lastFRUData) == 0 {
		return map[string]interface{}{"success": false, "message": "请先导出 FRU 数据"}
	}
	date := time.Now().Format("20060102")
	filename := fmt.Sprintf("%s_%s.bin", serial, date)
	file := filepath.Join(dataDir, "fru", filename)
	if err := os.WriteFile(file, lastFRUData, 0644); err != nil {
		return map[string]interface{}{"success": false, "message": "保存失败: " + err.Error()}
	}
	a.emitLog(fmt.Sprintf("FRU 已保存: %s", file))
	return map[string]interface{}{"success": true, "file": file, "message": "保存成功"}
}

// fruToMap 将库返回的 ipmi.FRU 转为 map[string]string 供前端展示
func fruToMap(fru *ipmi.FRU) map[string]string {
	m := make(map[string]string)
	if fru == nil {
		return m
	}

	if c := fru.ChassisInfoArea; c != nil {
		m["Chassis_Type"] = c.ChassisType.String()
		if v := bytesToStr(c.PartNumber); v != "" {
			m["Chassis_PartNumber"] = v
		}
		if v := bytesToStr(c.SerialNumber); v != "" {
			m["Chassis_SerialNumber"] = v
		}
	}

	if b := fru.BoardInfoArea; b != nil {
		if !b.MfgDateTime.IsZero() {
			m["Board_MfgDate"] = b.MfgDateTime.Format("2006-01-02 15:04")
		}
		if v := bytesToStr(b.Manufacturer); v != "" {
			m["Board_Manufacturer"] = v
		}
		if v := bytesToStr(b.ProductName); v != "" {
			m["Board_ProductName"] = v
		}
		if v := bytesToStr(b.SerialNumber); v != "" {
			m["Board_SerialNumber"] = v
		}
		if v := bytesToStr(b.PartNumber); v != "" {
			m["Board_PartNumber"] = v
		}
		if v := bytesToStr(b.FRUFileID); v != "" {
			m["Board_FRUFileID"] = v
		}
	}

	if p := fru.ProductInfoArea; p != nil {
		if v := bytesToStr(p.Manufacturer); v != "" {
			m["Product_Manufacturer"] = v
		}
		if v := bytesToStr(p.Name); v != "" {
			m["Product_ProductName"] = v
		}
		if v := bytesToStr(p.PartModel); v != "" {
			m["Product_PartNumber"] = v
		}
		if v := bytesToStr(p.Version); v != "" {
			m["Product_Version"] = v
		}
		if v := bytesToStr(p.SerialNumber); v != "" {
			m["Product_SerialNumber"] = v
		}
		if v := bytesToStr(p.AssetTag); v != "" {
			m["Product_AssetTag"] = v
		}
		if v := bytesToStr(p.FRUFileID); v != "" {
			m["Product_FRUFileID"] = v
		}
		for i, c := range p.Custom {
			if s := bytesToStr(c); s != "" {
				m[fmt.Sprintf("Product_Extra_%d", i)] = s
			}
		}
	}

	for i, rec := range fru.MultiRecords {
		key := fmt.Sprintf("MultiRecord_%d_Type_0x%02x", i, rec.RecordType)
		m[key] = fmt.Sprintf("%x", rec.RecordData)
	}

	return m
}

func (a *App) ImportFRU(host, user, pass, file string, fields map[string]string) map[string]interface{} {
	c, err := ipmi.NewClient(host, 623, user, pass)
	if err != nil {
		return map[string]interface{}{"success": false, "message": "连接失败: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		return map[string]interface{}{"success": false, "message": "连接失败: " + err.Error()}
	}
	defer c.Close(ctx)

	// 读取导出时保存的 .bin 文件作为基座；失败则从 BMC 实时读取
	src, err := os.ReadFile(file)
	if err != nil || len(src) == 0 {
		a.emitLog(fmt.Sprintf("bin文件读取失败(file=%q, err=%v)，改为从BMC实时读取", file, err))
		src, _ = readFRUFull(ctx, c)
	}
	if len(src) == 0 {
		return map[string]interface{}{"success": false, "message": "读取 FRU 文件失败"}
	}
	// 获取 BMC 上的 FRU 大小，确保数据完整
	info, err := c.GetFRUInventoryAreaInfo(ctx, 0)
	if err == nil && info != nil && info.AreaSizeBytes > 0 {
		bufSize := int(info.AreaSizeBytes)
		if bufSize > 8192 && bufSize*8 <= 65535 {
			bufSize *= 8
		}
		if bufSize > 65535 {
			bufSize = 65535
		}
		if len(src) < bufSize {
			// 补零到完整大小
			padded := make([]byte, bufSize)
			copy(padded, src)
			src = padded
		}
	}

	dst := applyFRUFields(src, fields, a.emitLog)

	// 写回整个 FRU 区域
	for off := 0; off < len(dst); off += 16 {
		end := off + 16
		if end > len(dst) {
			end = len(dst)
		}
		if _, err := c.WriteFRUData(ctx, 0, uint16(off), dst[off:end]); err != nil {
			return map[string]interface{}{"success": false, "message": fmt.Sprintf("写入失败 offset=%d: %v", off, err)}
		}
	}
	a.emitLog(fmt.Sprintf("FRU 导入: %s", host))
	return map[string]interface{}{"success": true, "message": "导入成功"}
}

func (a *App) OpenFRUFile(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{"success": false, "message": "无法打开文件"}
	}
	fields := parseFRUBytes(data)
	return map[string]interface{}{"success": true, "file": path, "fields": fields}
}

// ─── 自定义 IPMI ──────────────────────────────────────────────────

type IPMIButton struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type AppConfig struct {
	IPMIButtons  []IPMIButton `json:"ipmiButtons"`
	DefaultIface string       `json:"defaultIface"`
}

func (a *App) loadConfig() AppConfig {
	path := filepath.Join(dataDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}
	}
	var cfg AppConfig
	json.Unmarshal(data, &cfg)
	return cfg
}

func (a *App) GetConfig() map[string]interface{} {
	cfg := a.loadConfig()
	a.emitLog(fmt.Sprintf("配置加载: DefaultIface=%s, IPMIButtons=%d", cfg.DefaultIface, len(cfg.IPMIButtons)))
	return map[string]interface{}{
		"DefaultIface": cfg.DefaultIface,
		"IPMIButtons":  cfg.IPMIButtons,
	}
}

func (a *App) SaveIPMIButtons(buttons []IPMIButton) map[string]interface{} {
	cfg := a.loadConfig()
	cfg.IPMIButtons = buttons
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return map[string]interface{}{"success": false, "message": err.Error()}
	}
	path := filepath.Join(dataDir, "config.json")
	os.WriteFile(path, data, 0644)
	a.emitLog(fmt.Sprintf("IPMI按钮已保存: %d 个", len(buttons)))
	return map[string]interface{}{"success": true}
}

func (a *App) SetDefaultIface(iface string) map[string]interface{} {
	cfg := a.loadConfig()
	cfg.DefaultIface = iface
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return map[string]interface{}{"success": false, "message": err.Error()}
	}
	path := filepath.Join(dataDir, "config.json")
	os.WriteFile(path, data, 0644)
	a.emitLog(fmt.Sprintf("默认网卡已保存: %s", iface))
	return map[string]interface{}{"success": true}
}

func (a *App) ExecuteIPMI(host, user, pass, command string) map[string]interface{} {
	c, err := ipmi.NewClient(host, 623, user, pass)
	if err != nil {
		return map[string]interface{}{"success": false, "message": "连接失败"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		return map[string]interface{}{"success": false, "message": "连接失败: " + err.Error()}
	}
	defer c.Close(ctx)

	lower := strings.ToLower(strings.TrimSpace(command))

	// 自动为 raw 格式补充前缀: raw 0a 44 ...
	if strings.HasPrefix(lower, "raw ") {
		rawParts := strings.Fields(command)[1:] // 去掉 "raw"
		rawBytes := parseHexArgs(rawParts)
		if len(rawBytes) < 2 {
			return map[string]interface{}{"success": false, "message": "raw 格式: raw netfn cmd [data...]"}
		}
		r, err := c.RawCommand(ctx, ipmi.NetFn(rawBytes[0]), rawBytes[1], rawBytes[2:], "")
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": fmt.Sprintf("%x", r.Response)}
	}

	switch lower {
	// ====== sel ======
	case "sel elist", "sel list":
		entries, err := c.GetSELEntries(ctx, 0)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": ipmi.FormatSELs(entries, nil)}
	case "sel info":
		res, err := c.GetSELInfo(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "sel time":
		res, err := c.GetSELTime(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "sel clear":
		res, err := c.ClearSEL(ctx, 0)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== mc / bmc ======
	case "mc info", "bmc info":
		res, err := c.GetDeviceID(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "mc guid", "bmc guid":
		res, err := c.GetDeviceGUID(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "mc reset cold", "bmc cold":
		if err := c.ColdReset(ctx); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "BMC cold reset OK"}
	case "mc reset warm", "bmc warm":
		if err := c.WarmReset(ctx); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "BMC warm reset OK"}
	case "mc selftest", "bmc selftest":
		res, err := c.GetSelfTestResults(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "mc getsysguid", "bmc getsysguid":
		res, err := c.GetSystemGUID(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== chassis ======
	case "chassis status":
		res, err := c.GetChassisStatus(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "chassis poh":
		res, err := c.GetPOHCounter(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "chassis power on", "power on":
		if _, err := c.ChassisControl(ctx, ipmi.ChassisControlPowerUp); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Power on OK"}
	case "chassis power off", "power off":
		if _, err := c.ChassisControl(ctx, ipmi.ChassisControlPowerDown); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Power off OK"}
	case "chassis power cycle", "power cycle":
		if _, err := c.ChassisControl(ctx, ipmi.ChassisControlPowerCycle); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Power cycle OK"}
	case "chassis power reset", "power reset":
		if _, err := c.ChassisControl(ctx, ipmi.ChassisControlHardReset); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Power reset OK"}
	case "chassis power soft", "power soft":
		if _, err := c.ChassisControl(ctx, ipmi.ChassisControlSoftShutdown); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Soft shutdown OK"}
	case "chassis restart cause":
		res, err := c.GetSystemRestartCause(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "chassis identify":
		if _, err := c.ChassisIdentify(ctx, 15, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Identify LED 15s"}
	case "chassis identify off":
		if _, err := c.ChassisIdentify(ctx, 0, true); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Identify LED off"}

	// ====== lan / network ======
	case "lan print", "lan print 1":
		cfg, err := c.GetLanConfig(ctx, 1)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": cfg.Format()}
	case "lan stats":
		res, err := c.GetIPStatistics(ctx, 1, false)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== channel ======
	case "channel info", "channel info 1":
		res, err := c.GetChannelInfo(ctx, 1)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "channel access":
		res, err := c.GetChannelAccess(ctx, 1, ipmi.ChannelAccessOption_Volatile)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== fru ======
	case "fru print", "fru list", "fru list 0":
		res, err := c.GetFRU(ctx, 0, "")
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.String()}
	case "fru info":
		res, err := c.GetFRUInventoryAreaInfo(ctx, 0)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== sensor ======
	case "sensor list":
		sensors, err := c.GetSensors(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": fmt.Sprintf("%d sensors found", len(sensors))}

	// ====== sdr ======
	case "sdr list", "sdr elist":
		sdrs, err := c.GetSDRs(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": fmt.Sprintf("%d SDRs found", len(sdrs))}
	case "sdr info":
		res, err := c.GetSDRRepoInfo(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== user ======
	case "user list", "user list 1":
		var users []map[string]string
		for uid := uint8(1); uid <= 15; uid++ {
			uname, err := c.GetUsername(ctx, uid)
			if err != nil {
				continue
			}
			if uname.Username == "" {
				continue
			}
			uacc, err := c.GetUserAccess(ctx, 1, uid)
			if err != nil {
				continue
			}
			users = append(users, map[string]string{
				"ID":       strconv.Itoa(int(uid)),
				"Name":     uname.Username,
				"LinkAuth": boolToStr(uacc.LinkAuthEnabled),
				"IPMI":     boolToStr(uacc.IPMIMessagingEnabled),
				"Priv":     uacc.MaxPrivLevel.String(),
			})
		}
		return map[string]interface{}{"success": true, "output": ipmi.RenderTable([]string{"ID", "Name", "LinkAuth", "IPMI", "Priv"}, users)}

	// ====== watchdog ======
	case "watchdog get":
		res, err := c.GetWatchdogTimer(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== dcmi ======
	case "dcmi power":
		res, err := c.GetDCMIPowerReading(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "dcmi temp":
		res, err := c.GetDCMITemperatureReadings(ctx, &ipmi.GetDCMITemperatureReadingsRequest{})
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	case "dcmi sensor":
		res, err := c.GetDCMISensorInfo(ctx, &ipmi.GetDCMISensorInfoRequest{})
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== boot ======
	case "bootdev bios":
		if err := c.SetBootDevice(ctx, ipmi.BootDeviceSelectorForceBIOSSetup, ipmi.BIOSBootTypeLegacy, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Next boot: BIOS setup"}
	case "bootdev pxe":
		if err := c.SetBootDevice(ctx, ipmi.BootDeviceSelectorForcePXE, ipmi.BIOSBootTypeLegacy, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Next boot: PXE"}
	case "bootdev cd":
		if err := c.SetBootDevice(ctx, ipmi.BootDeviceSelectorForceCDROM, ipmi.BIOSBootTypeLegacy, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Next boot: CD/DVD"}
	case "bootdev disk":
		if err := c.SetBootDevice(ctx, ipmi.BootDeviceSelectorForceHardDrive, ipmi.BIOSBootTypeLegacy, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Next boot: Hard Disk"}
	case "bootdev floppy":
		if err := c.SetBootDevice(ctx, ipmi.BootDeviceSelectorForceFloppy, ipmi.BIOSBootTypeLegacy, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Next boot: Floppy"}
	case "bootdev none":
		if err := c.SetBootDevice(ctx, ipmi.BootDeviceSelectorNoOverride, ipmi.BIOSBootTypeLegacy, false); err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": "Boot override cleared"}

	// ====== session ======
	case "session info active":
		res, err := c.GetCurrentSessionInfo(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}

	// ====== ping ======
	case "rmcp ping":
		res, err := c.RmcpPing(ctx)
		if err != nil {
			return map[string]interface{}{"success": false, "message": err.Error()}
		}
		return map[string]interface{}{"success": true, "output": res.Format()}
	}

	// 回退：直接十六进制 netfn cmd [data ...]
	parts := strings.Fields(command)
	raw := parseHexArgs(parts)
	if len(raw) < 2 {
		return map[string]interface{}{"success": false, "message": "未知命令: " + command + "\n\n" + ipmiHelp()}
	}
	r, err := c.RawCommand(ctx, ipmi.NetFn(raw[0]), raw[1], raw[2:], "")
	if err != nil {
		return map[string]interface{}{"success": false, "message": err.Error()}
	}
	return map[string]interface{}{"success": true, "output": fmt.Sprintf("%x", r.Response)}
}

func boolToStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func parseHexArgs(args []string) []byte {
	var buf []byte
	for _, a := range args {
		a = strings.TrimPrefix(a, "0x")
		a = strings.TrimPrefix(a, "0X")
		var v uint8
		if _, err := fmt.Sscanf(a, "%02x", &v); err != nil {
			continue
		}
		buf = append(buf, v)
	}
	return buf
}

func ipmiHelp() string {
	return `支持的命令:
  sel elist / sel info / sel time / sel clear
  mc info / mc guid / mc reset cold / mc reset warm / mc selftest
  chassis status / chassis poh
  chassis power on|off|cycle|reset|soft / chassis restart cause
  chassis identify / chassis identify off
  lan print / lan stats
  channel info / channel access
  fru print / fru info
  sensor list / sdr list / sdr info
  user list / watchdog get
  dcmi power / dcmi temp / dcmi sensor
  bootdev bios|pxe|cd|disk|none / rmcp ping
  session info active
  raw netfn cmd [data...]
  或直接输入十六进制: 06 01`
}

// ─── FRU 解析 — 使用库内置类型 ──────────────────────────────────

// bytesToStr 将 []byte 转字符串，nil/空返回空串
func bytesToStr(b []byte) string {
	if b == nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readFRUFull 读取 deviceID=0 的完整 FRU 数据
func readFRUFull(ctx context.Context, c *ipmi.Client) ([]byte, error) {
	return readFRUArea(ctx, c, 0)
}

// readFRUArea 读取整块 FRU 数据。
//
// 注意两点（原实现会在这里出错）：
//   - AreaSizeBytes 已经是字节数；个别 BMC 返回的是 (n+1)*8 编码值，
//     只有展开后仍不超过 65535 才展开，否则 IPMI 的 uint16 偏移会回绕，
//     变成从头重复读取甚至死循环；
//   - CountReturned 才是有效字节数，Data 可能被 BMC 补齐，按 CountReturned 截断。
func readFRUArea(ctx context.Context, c *ipmi.Client, deviceID uint8) ([]byte, error) {
	info, err := c.GetFRUInventoryAreaInfo(ctx, deviceID)
	if err != nil || info == nil || info.AreaSizeBytes < 1 {
		return nil, fmt.Errorf("FRU 信息获取失败")
	}

	totalBytes := int(info.AreaSizeBytes)
	if totalBytes > 8192 && totalBytes*8 <= 65535 {
		totalBytes *= 8
	}
	if totalBytes > 65535 {
		totalBytes = 65535
	}

	var data []byte
	for off := 0; off < totalBytes; {
		count := uint8(32)
		if totalBytes-off < int(count) {
			count = uint8(totalBytes - off)
		}
		r, err := c.ReadFRUData(ctx, deviceID, uint16(off), count)
		if err != nil || r == nil || len(r.Data) == 0 || r.CountReturned == 0 {
			break
		}
		n := int(r.CountReturned)
		if n > len(r.Data) {
			n = len(r.Data)
		}
		data = append(data, r.Data[:n]...)
		off += n
		if int(r.CountReturned) < int(count) {
			break
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("FRU 数据为空")
	}
	return data, nil
}

func parseFRUBytes(data []byte) map[string]string {
	fields := make(map[string]string)

	// 解析 Common Header
	hdr := &ipmi.FRUCommonHeader{}
	if err := hdr.Unpack(data); err != nil {
		return nil
	}

	// ── Chassis Info Area ──
	if off := uint16(hdr.ChassisOffset8B) * 8; off > 0 && int(off) < len(data) {
		area := &ipmi.FRUChassisInfoArea{}
		if err := area.Unpack(data[off:]); err == nil {
			fields["Chassis_Type"] = area.ChassisType.String()
			if v := bytesToStr(area.PartNumber); v != "" {
				fields["Chassis_PartNumber"] = v
			}
			if v := bytesToStr(area.SerialNumber); v != "" {
				fields["Chassis_SerialNumber"] = v
			}
		}
	}

	// ── Board Info Area ──
	if off := uint16(hdr.BoardOffset8B) * 8; off > 0 && int(off) < len(data) {
		area := &ipmi.FRUBoardInfoArea{}
		if err := area.Unpack(data[off:]); err == nil {
			if !area.MfgDateTime.IsZero() {
				fields["Board_MfgDate"] = area.MfgDateTime.Format("2006-01-02 15:04")
			}
			if v := bytesToStr(area.Manufacturer); v != "" {
				fields["Board_Manufacturer"] = v
			}
			if v := bytesToStr(area.ProductName); v != "" {
				fields["Board_ProductName"] = v
			}
			if v := bytesToStr(area.SerialNumber); v != "" {
				fields["Board_SerialNumber"] = v
			}
			if v := bytesToStr(area.PartNumber); v != "" {
				fields["Board_PartNumber"] = v
			}
			if v := bytesToStr(area.FRUFileID); v != "" {
				fields["Board_FRUFileID"] = v
			}
		}
	}

	// ── Product Info Area ──
	if off := uint16(hdr.ProductOffset8B) * 8; off > 0 && int(off) < len(data) {
		area := &ipmi.FRUProductInfoArea{}
		if err := area.Unpack(data[off:]); err == nil {
			if v := bytesToStr(area.Manufacturer); v != "" {
				fields["Product_Manufacturer"] = v
			}
			if v := bytesToStr(area.Name); v != "" {
				fields["Product_ProductName"] = v
			}
			if v := bytesToStr(area.PartModel); v != "" {
				fields["Product_PartNumber"] = v
			}
			if v := bytesToStr(area.Version); v != "" {
				fields["Product_Version"] = v
			}
			if v := bytesToStr(area.SerialNumber); v != "" {
				fields["Product_SerialNumber"] = v
			}
			if v := bytesToStr(area.AssetTag); v != "" {
				fields["Product_AssetTag"] = v
			}
			if v := bytesToStr(area.FRUFileID); v != "" {
				fields["Product_FRUFileID"] = v
			}
			for i, c := range area.Custom {
				if s := bytesToStr(c); s != "" {
					fields[fmt.Sprintf("Product_Extra_%d", i)] = s
				}
			}
		}
	}

	// ── MultiRecord Area ──
	if off := uint16(hdr.MultiRecordsOffset8B) * 8; off > 0 && int(off) < len(data) {
		records, err := parseMultiRecords(data[off:])
		if err == nil {
			for i, rec := range records {
				key := fmt.Sprintf("MultiRecord_%d_Type_0x%02x", i, rec.RecordType)
				fields[key] = fmt.Sprintf("%x", rec.RecordData)
			}
		}
	}

	return fields
}

func parseMultiRecords(data []byte) ([]*ipmi.FRUMultiRecord, error) {
	var records []*ipmi.FRUMultiRecord
	off := 0
	for off+5 < len(data) {
		rec := &ipmi.FRUMultiRecord{}
		if err := rec.Unpack(data[off:]); err != nil {
			break
		}
		records = append(records, rec)
		if rec.EndOfList {
			break
		}
		off += 5 + int(rec.RecordLength)
	}
	return records, nil
}

// parseByte 解析 hex(0x17) 或 decimal(23) 字符串为 byte
func parseByte(s string) byte {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	s = strings.TrimPrefix(s, "0X")
	var v int
	fmt.Sscanf(s, "%x", &v)
	return byte(v)
}

// fruChecksum 计算 FRU 校验和（两's complement 加和）
func fruChecksum(data []byte, length int) byte {
	if length > len(data) {
		length = len(data)
	}
	var sum int
	for i := 0; i < length; i++ {
		sum += int(data[i])
	}
	return byte((256 - (sum % 256)) % 256)
}

// applyFRUFields 修改 Chassis/Board/Product 三区 TLV 字段，重算校验和
func applyFRUFields(src []byte, fields map[string]string, logFn func(string)) []byte {
	hdr := &ipmi.FRUCommonHeader{}
	if err := hdr.Unpack(src); err != nil {
		return src
	}

	dst := make([]byte, len(src))
	copy(dst, src)
	modified := false

	// ── Chassis Info Area ──
	if off := int(hdr.ChassisOffset8B) * 8; off > 0 && off+3 < len(src) {
		areaLen := int(src[off+1]) * 8
		end := off + areaLen
		if areaLen < 8 || end > len(src) {
			logFn("Chassis 区域长度异常（数据可能不完整），已跳过")
			areaLen = 0
		}
		// Chassis_Type 固定位置 (byte 2)，非 TLV
		if areaLen == 0 {
			// 区域不完整，跳过本区
		} else {
			if v, ok := fields["Chassis_Type"]; ok {
				n := parseByte(v)
				if n > 0 && dst[off+2] != n {
					dst[off+2] = n
					modified = true
					logFn(fmt.Sprintf("Chassis_Type -> 0x%02x", n))
				}
			}
			p := dst[off+3 : end-1]
			tlvMod := modifyTLV(p, fields, "Chassis_", []string{"PartNumber", "SerialNumber"}, logFn)
			if tlvMod {
				dst[end-1] = fruChecksum(dst[off:end], areaLen-1)
				modified = true
			} else if _, ok := fields["Chassis_Type"]; ok {
				// Chassis_Type 变了也需要重算校验和
				dst[end-1] = fruChecksum(dst[off:end], areaLen-1)
				modified = true
			}
		}
	}

	// ── Board Info Area ──
	if off := int(hdr.BoardOffset8B) * 8; off > 0 && off+6 < len(src) {
		areaLen := int(src[off+1]) * 8
		end := off + areaLen
		if areaLen < 8 || end > len(src) {
			logFn("Board 区域长度异常（数据可能不完整），已跳过")
			areaLen = 0
		}
		if areaLen == 0 {
			// 区域不完整，跳过本区
		} else {
			p := dst[off+6 : end-1] // TLV starts after version(1)+length(1)+lang(1)+mfgdate(3)
			m := modifyTLV(p, fields, "Board_", []string{"Manufacturer", "ProductName", "SerialNumber", "PartNumber", "FRUFileID"}, logFn)
			if m {
				dst[end-1] = fruChecksum(dst[off:end], areaLen-1)
				modified = true
			}
		}
	}

	// ── Product Info Area ──
	if off := int(hdr.ProductOffset8B) * 8; off > 0 && off+3 < len(src) {
		areaLen := int(src[off+1]) * 8
		end := off + areaLen
		if areaLen < 8 || end > len(src) {
			logFn("Product 区域长度异常（数据可能不完整），已跳过")
			areaLen = 0
		}
		if areaLen == 0 {
			// 区域不完整，跳过本区
		} else {
			p := dst[off+3 : end-1]
			m := modifyTLV(p, fields, "Product_", []string{"Manufacturer", "ProductName", "PartNumber", "Version", "SerialNumber", "AssetTag", "FRUFileID", "Extra_0", "Extra_1"}, logFn)
			if m {
				dst[end-1] = fruChecksum(dst[off:end], areaLen-1)
				modified = true
			}
		}
	}

	if !modified {
		return dst
	}

	// 重新计算 Common Header 校验和
	dst[6] = 0x00
	dst[7] = fruChecksum(dst, 7)
	return dst
}

// modifyTLV 在 TLV 数据 p 中查找 nameList 对应的字段并替换
func modifyTLV(p []byte, fields map[string]string, prefix string, nameList []string, logFn func(string)) bool {
	modified := false
	typeIdx := 0
	i := 0
	for i < len(p) {
		tl := p[i]
		origLen := int(tl & 0x3F)
		i++
		if i+origLen > len(p) {
			break
		}
		if origLen == 0 {
			typeIdx++
			continue
		}
		var fieldKey string
		if typeIdx < len(nameList) {
			fieldKey = prefix + nameList[typeIdx]
		}
		if fieldKey != "" {
			curVal := string(p[i : i+origLen])
			if newVal, ok := fields[fieldKey]; ok && newVal != curVal {
				b := []byte(newVal)
				for j := 0; j < origLen; j++ {
					p[i+j] = ' '
				}
				n := len(b)
				if n > origLen {
					// FRU 字段长度由区域布局固定，超长只能截断（改长度需要重建整个区域）
					logFn(fmt.Sprintf("%s 新值 %d 字节超过字段长度 %d，已截断", fieldKey, n, origLen))
					n = origLen
				}
				copy(p[i:], b[:n])
				modified = true
				logFn(fmt.Sprintf("%s '%s' -> '%s'", fieldKey, curVal, string(p[i:i+origLen])))
			}
		}
		i += origLen
		typeIdx++
	}
	return modified
}

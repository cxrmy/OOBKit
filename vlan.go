package main

import (
	"fmt"
	"strings"
)

// ─── 网卡 VLAN（802.1Q 标签）─────────────────────────────────────
//
// 实现思路：
//   1. 优先使用驱动暴露的高级属性（Get/Set-NetAdapterAdvancedProperty），
//      关键字通常是 VlanID / RegVlanid / VLANID，具体名字因厂商而异，
//      这里通过 RegistryKeyword 与 DisplayName 双重匹配，把真实关键字取出来；
//   2. 驱动没有暴露属性时，回退到驱动的注册表键（VlanID），
//      该键存在说明驱动认这个参数，写入后重启网卡生效；
//   3. 应用后按需重启网卡（Restart-NetAdapter，失败则退化为 netsh 禁用/启用）。

// VLANAdapter 描述一块网卡的 VLAN 能力与当前取值
type VLANAdapter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MAC         string `json:"mac"`
	Status      string `json:"status"`
	Keyword     string `json:"keyword"`   // 驱动暴露的 VLAN 关键字，空表示不支持
	VLANID      string `json:"vlanId"`    // 当前 VLAN ID（可能为空）
	Supported   bool   `json:"supported"` // 驱动是否支持设置 VLAN ID
}

// 网卡驱动注册表键的通用类 GUID
const vlanClassKey = "HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Class\\{4d36e972-e325-11ce-bfc1-08002be10318}"

// psQuote 把字符串安全地嵌入 PowerShell 单引号字面量
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// GetVLANAdapters 枚举所有网卡，并读取每块网卡的 VLAN ID 能力与当前值
func (a *App) GetVLANAdapters() []VLANAdapter {
	script := `$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[Text.Encoding]::UTF8; ` +
		`$classKey=` + psQuote(vlanClassKey) + `; ` +
		`Get-NetAdapter | Sort-Object Name | ForEach-Object { ` +
		`$name=$_.Name; $guid=$_.InterfaceGuid; ` +
		`$props=@(Get-NetAdapterAdvancedProperty -Name $name -ErrorAction SilentlyContinue | Where-Object { $_.RegistryKeyword -match '(?i)^(reg)?vlan\s*id$' }); ` +
		`if($props.Count -eq 0){ $props=@(Get-NetAdapterAdvancedProperty -Name $name -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -match '(?i)^vlan\s*(id)?$' }) }; ` +
		`$kw=''; $val=''; ` +
		`if($props.Count -gt 0){ $kw=$props[0].RegistryKeyword; $val=[string]$props[0].RegistryValue }; ` +
		`if($kw -eq ''){ $key=Get-ChildItem $classKey -ErrorAction SilentlyContinue | Where-Object { $_.GetValue('NetCfgInstanceId') -eq $guid } | Select-Object -First 1; ` +
		`if($key){ $reg=$key.GetValue('VlanID'); if($null -ne $reg){ $kw='VlanID@reg'; $val=[string]$reg } } }; ` +
		`$name+'|'+$_.InterfaceDescription+'|'+$_.MacAddress+'|'+$_.Status+'|'+$kw+'|'+$val }`

	out, err := execPowerShellHidden(encodePowerShellCmd(script))
	if err != nil && len(strings.TrimSpace(string(out))) == 0 {
		a.emitLog("枚举网卡 VLAN 能力失败: " + err.Error())
		return nil
	}

	var list []VLANAdapter
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.Contains(line, "xmlns") {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) < 6 {
			continue
		}
		kw := strings.TrimSpace(parts[4])
		list = append(list, VLANAdapter{
			Name:        strings.TrimSpace(parts[0]),
			Description: strings.TrimSpace(parts[1]),
			MAC:         strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(parts[2]), "-", ":")),
			Status:      strings.TrimSpace(parts[3]),
			Keyword:     kw,
			VLANID:      strings.TrimSpace(parts[5]),
			Supported:   kw != "",
		})
	}

	supported := 0
	for _, ad := range list {
		if ad.Supported {
			supported++
		}
	}
	a.emitLog(fmt.Sprintf("网卡 VLAN 枚举完成: %d 块，其中 %d 块支持 VLAN ID", len(list), supported))
	return list
}

// resolveIfaceName 把 pcap 设备名 / 友好名统一成 netsh & PowerShell 可用的网卡名
func resolveIfaceName(iface string) string {
	iface = strings.TrimSpace(iface)
	if iface == "" {
		return ""
	}
	for _, ad := range adapCache {
		if strings.EqualFold(ad.ConnName, iface) {
			return ad.ConnName
		}
	}
	if name := getNetshName(iface); name != "" {
		return name
	}
	return iface
}

// SetVLAN 设置指定网卡的 VLAN ID（0 表示关闭 802.1Q 标签）
func (a *App) SetVLAN(iface string, vlanID int, restart bool) map[string]interface{} {
	if vlanID < 0 || vlanID > 4094 {
		return map[string]interface{}{"success": false, "message": "VLAN ID 必须为 0-4094（0 表示关闭）"}
	}
	name := resolveIfaceName(iface)
	if name == "" {
		return map[string]interface{}{"success": false, "message": "未指定网卡"}
	}

	a.emitLog(fmt.Sprintf("设置 VLAN: %s → %d (重启网卡=%v)", name, vlanID, restart))

	script := `$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[Text.Encoding]::UTF8; ` +
		`$n=` + psQuote(name) + `; $v='` + fmt.Sprintf("%d", vlanID) + `'; $restart=$` + fmt.Sprintf("%t", restart) + `; ` +
		`$classKey=` + psQuote(vlanClassKey) + `; ` +
		`$p=@(Get-NetAdapterAdvancedProperty -Name $n -ErrorAction SilentlyContinue | Where-Object { $_.RegistryKeyword -match '(?i)^(reg)?vlan\s*id$' }); ` +
		`if($p.Count -eq 0){ $p=@(Get-NetAdapterAdvancedProperty -Name $n -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -match '(?i)^vlan\s*(id)?$' }) }; ` +
		`if($p.Count -gt 0){ ` +
		`try{ Set-NetAdapterAdvancedProperty -Name $n -RegistryKeyword $p[0].RegistryKeyword -RegistryValue $v -NoRestart -ErrorAction Stop; 'OK|'+$p[0].RegistryKeyword } ` +
		`catch{ 'ERR|'+$_.Exception.Message; exit 3 } ` +
		`} else { ` +
		`$na=Get-NetAdapter -Name $n -ErrorAction SilentlyContinue; ` +
		`if(-not $na){ 'ERR|网卡不存在: '+$n; exit 4 }; ` +
		`$key=Get-ChildItem $classKey -ErrorAction SilentlyContinue | Where-Object { $_.GetValue('NetCfgInstanceId') -eq $na.InterfaceGuid } | Select-Object -First 1; ` +
		`if($key -and $null -ne $key.GetValue('VlanID')){ ` +
		`try{ Set-ItemProperty -Path $key.PSPath -Name 'VlanID' -Value $v -ErrorAction Stop; 'OK|VlanID@reg' } ` +
		`catch{ 'ERR|'+$_.Exception.Message; exit 3 } ` +
		`} else { 'ERR|该网卡驱动未提供 VLAN ID 属性（可更新驱动或改用支持 802.1Q 的网卡）'; exit 2 } ` +
		`}; ` +
		`if($restart){ ` +
		`try{ Restart-NetAdapter -Name $n -Confirm:$false -ErrorAction Stop; 'RESTART|Restart-NetAdapter' } ` +
		`catch{ try{ netsh interface set interface "$n" admin=disabled | Out-Null; Start-Sleep -Milliseconds 800; netsh interface set interface "$n" admin=enabled | Out-Null; 'RESTART|netsh' } catch { 'WARN|自动重启网卡失败，请手动禁用/启用该网卡' } } ` +
		`}`

	out, err := execPowerShellHidden(encodePowerShellCmd(script))

	var okKeyword, restartMsg, warnMsg, errMsg string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		switch {
		case strings.HasPrefix(line, "OK|"):
			okKeyword = strings.TrimPrefix(line, "OK|")
		case strings.HasPrefix(line, "RESTART|"):
			restartMsg = strings.TrimPrefix(line, "RESTART|")
		case strings.HasPrefix(line, "WARN|"):
			warnMsg = strings.TrimPrefix(line, "WARN|")
		case strings.HasPrefix(line, "ERR|"):
			errMsg = strings.TrimPrefix(line, "ERR|")
		}
	}

	if errMsg != "" {
		a.emitLog("设置 VLAN 失败: " + errMsg)
		return map[string]interface{}{"success": false, "message": errMsg}
	}
	if okKeyword == "" {
		msg := "设置失败：未获得驱动的确认结果"
		if err != nil {
			msg = "设置失败: " + err.Error()
		}
		a.emitLog(msg)
		return map[string]interface{}{"success": false, "message": msg}
	}

	msg := fmt.Sprintf("%s 的 VLAN 已设置为 %d（%s）", name, vlanID, okKeyword)
	if restartMsg != "" {
		msg += "，网卡已重启"
	}
	if warnMsg != "" {
		msg += "；" + warnMsg
	}
	a.emitLog(msg)
	return map[string]interface{}{
		"success":   true,
		"message":   msg,
		"keyword":   okKeyword,
		"restarted": restartMsg != "",
	}
}

// ClearVLAN 关闭指定网卡的 802.1Q 标签（VLAN ID 置 0）
func (a *App) ClearVLAN(iface string, restart bool) map[string]interface{} {
	return a.SetVLAN(iface, 0, restart)
}

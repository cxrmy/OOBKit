# 更新日志

本文件记录 OOBKit 的重要变更。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 新增

- 网卡 VLAN：新增「网卡 VLAN」页，自动识别驱动暴露的 VLAN ID 关键字（VlanID / RegVlanid 等），
  支持设置、清除 802.1Q 标签，可选择应用后重启网卡；驱动未暴露属性时回退到注册表 VlanID

### 修复（DHCP 服务器）

- 服务端 IP 不再固定回落到 192.168.1.1：改为读取所选网卡的真实 IPv4，并校验其与地址池同网段
- 应答改发本网段定向广播（如 192.168.1.255），修复多网卡（WLAN + 有线）时 Offer/ACK 从错误网卡发出的问题
- 地址池严格限制在起始~结束之间，跳过网络地址、广播地址与本机地址；过期租约自动回收
- 处理 DHCPRELEASE / DHCPDECLINE，续租会刷新过期时间
- DHCPREQUEST 请求的地址与分配不一致时回 DHCPNAK，避免客户端拿到错误地址
- 单次发送失败不再导致整个 DHCP 服务退出；启动/停止状态可通过 GetDHCPStatus 查询
- 启动前校验起始/结束 IP、掩码、网关与网卡 IP 的网段关系，报错信息明确

### 修复（稳定性，代码审查发现）

- 扫描/监听：`StopAll` 改为关闭停止通道（广播），修掉「同时开扫描和监听时只能停掉一个、另一个永远停不下来」的问题
- 被动监听：抓包通道关闭（网卡被禁用/拔出）时 `Packets()` 会返回 nil，原代码会一直空转并最终崩溃；打开网卡失败也会正确回传前端提示
- FRU：区域长度字段异常或 .bin 被截断时不再切片越界崩溃（已补回归测试）；FRU 读取增加长度上限，避免 IPMI 的 uint16 偏移回绕
- IPMI：连接与命令增加超时（120s / 60s），BMC 不可达时不再让界面永久卡住
- DHCP：DISCOVER 阶段只预留 2 分钟，未被确认的 Offer 能快速回收，不会把地址池占满
- 恢复 DHCP 时对网卡名做 PowerShell 转义，名称含单引号也不再拼坏命令

### 改进

- 一键配网支持自定义子网掩码：探测器页新增「掩码」输入，并按掩码自动推算同网段的可用本机 IP
- DHCP 页「自定义本机 IP」可单独填写掩码，留空则沿用 DHCP 子网掩码；与地址池不同网段时会二次确认
- 配网与启动 DHCP 前统一校验 IP / 掩码格式，后端 SetStaticIP 增加参数校验，报错信息更明确

## [2.1.0] - 2026-08-17

### 新增

- 被动识别 mDNS/DHCP/LLDP 设备，BMC 无需拔插网线即可被发现
  - mDNS：监听 UDP 5353，识别 iDRAC/iLO/iBMC/XCC 等服务的周期通告
  - DHCP：监听 UDP 67/68，提取 MAC、IP、主机名
  - LLDP：解析链路对端通告，记录日志辅助直连判断

### 修复

- CI：移除 Windows 上 Go 依赖缓存导致的 tar 路径报错

## [2.0.1] - 2026-08-15

### 新增

- 首个发布版本
- BMC 探测器：ARP 扫描/被动监听，识别厂商（浪潮、华为、Dell、HPE 等），一键配网
- DHCP 服务器：内置轻量服务，租约表格实时显示
- FRU / IPMI：FRU 导出/导入/保存，30+ IPMI 命令别名及 raw 十六进制
- GitHub Actions 自动构建与 Release 发布

### 修复

- Windows 图标路径（Wails v2 从 build/appicon.png 读取）
- 链接改用系统默认浏览器打开（runtime.BrowserOpenURL）

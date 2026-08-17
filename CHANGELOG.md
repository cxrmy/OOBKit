# 更新日志

本文件记录 OOBKit 的重要变更。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

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

//go:build windows

package main

import (
	"net"
	"time"
)

// newUDP67Conn 建立 DHCP 服务端 socket。
//
// 必须绑定 0.0.0.0:67：DHCP 客户端在拿到地址前以 0.0.0.0 为源、255.255.255.255
// 为目的广播，只有绑定到全部地址的 socket 才能收到这类广播；绑定到具体网卡地址
// 会收不到。因此 bindIP 只用于记录/诊断，应答从哪块网卡发出由「定向广播」决定
// （见 DHCPServer.replyAddr），这样多网卡机器上才不会把 Offer 从 WLAN 发出去。
func newUDP67Conn(bindIP net.IP) (net.PacketConn, error) {
	_ = bindIP
	var err error
	for i := 0; i < 4; i++ {
		var conn net.PacketConn
		conn, err = net.ListenPacket("udp4", ":67")
		if err == nil {
			return conn, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil, err
}

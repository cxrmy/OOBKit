//go:build windows

package main

import (
	"net"
	"time"
)

func newUDP67Conn(bindIP net.IP) (net.PacketConn, error) {
	conn, err := net.ListenPacket("udp4", ":67")
	if err != nil {
		time.Sleep(500 * time.Millisecond)
		conn, err = net.ListenPacket("udp4", ":67")
	}
	return conn, err
}

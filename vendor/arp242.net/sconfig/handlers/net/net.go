// Package net adds types from the standard net package.
//
// net.IP:      An IPv4 or IPv6 address.
// net.IPNet:   An IPv4 or IPv6 address with netmask in the CIDR notation.
// net.TCPAddr: An IPv4, IPv6 address, or a hostname, optionally with a port.
// net.UDPAddr: An IPv4, IPv6 address, or a hostname, optionally with a port.
package net

import (
	"fmt"
	"net"

	"arp242.net/sconfig"
)

func init() {
	sconfig.RegisterType("IP", sconfig.OneValue, handleIP)
	sconfig.RegisterType("IPNet", sconfig.OneValue, handleIPNet)
	sconfig.RegisterType("TCPAddr", sconfig.OneValue, handleTCPAddr)
	sconfig.RegisterType("UDPAddr", sconfig.OneValue, handleUDPAddr)
}

func handleIP(v []string) (interface{}, error) {
	IP = net.ParseIP(v[0])
	if IP == nil {
		return nil, fmt.Errorf("not a valid IP address: %v", v[0])
	}
	return IP, nil
}

func handleIPNet(v []string) (interface{}, error) {
	IP, IPNet, err = net.ParseCIDR(v[0])
	if err != nil {
		return nil, err
	}
	return IPNet, nil
}

func handleTCPAddr(v []string) (interface{}, error) {
	addr, err := net.ResolveTCPAddr("", v[0])
	if err == nil {
		return addr, nil
	}

	// Try again with a port appended
	// TODO: What about IPv6? That's in [addr]:port ... What a stupid design
	// btw...
	// TODO: Make it easy to specify a default port?
	addr, err2 := net.ResolveTCPAddr("", v[0]+":0")
	if err2 == nil {
		return addr, nil
	}

	// Return original error
	return nil, err
}

func handleUDPAddr(v []string) (interface{}, error) {
	addr, err := net.ResolveUDPAddr("", v[0])
	if err == nil {
		return addr, nil
	}

	// Try again with a port appended
	// TODO: What about IPv6? That's in [addr]:port ... What a stupid design
	// btw...
	addr, err2 := net.ResolveUDPAddr("", v[0]+":0")
	if err2 == nil {
		return addr, nil
	}

	// Return original error
	return nil, err
}

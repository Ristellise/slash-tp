package main

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pion/stun/v4"
)

func probeNAT() string {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return "probe failed: " + err.Error()
	}
	defer conn.Close()

	var mappings []string
	for _, server := range stunServers {
		raddr, err := net.ResolveUDPAddr("udp4", strings.TrimPrefix(server, "stun:"))
		if err != nil {
			continue
		}
		req := stun.MustBuild(stun.NewTransactionIDSetter(stun.NewTransactionID()), stun.BindingRequest)
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, err = conn.WriteToUDP(req.Raw, raddr); err != nil {
			continue
		}
		buf := make([]byte, 1500)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		res := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
		if err = res.Decode(); err != nil {
			continue
		}
		var x stun.XORMappedAddress
		if err = x.GetFrom(res); err != nil {
			continue
		}
		mappings = append(mappings, x.String())
	}

	switch {
	case len(mappings) == 0:
		return "unreachable (no STUN responses)"
	case len(mappings) == 1:
		return fmt.Sprintf("single response: %s (add more STUN servers to classify)", mappings[0])
	}
	for _, m := range mappings[1:] {
		if m != mappings[0] {
			return "endpoint-dependent (symmetric): " + strings.Join(mappings, ", ")
		}
	}
	return "endpoint-independent (cone): " + mappings[0]
}

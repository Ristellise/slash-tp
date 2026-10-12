package main

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pion/stun/v4"
)

// probeConn runs STUN binding queries from one socket to every STUN server.
func probeConn(conn *net.UDPConn) []string {
	type result struct {
		server  string
		mapping string
	}
	results := make(chan result, len(stunServers))
	for _, server := range stunServers {
		go func(server string) {
			raddr, err := net.ResolveUDPAddr("udp4", strings.TrimPrefix(server, "stun:"))
			if err != nil {
				results <- result{server, ""}
				return
			}
			req := stun.MustBuild(stun.NewTransactionIDSetter(stun.NewTransactionID()), stun.BindingRequest)
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err = conn.WriteToUDP(req.Raw, raddr); err != nil {
				results <- result{server, ""}
				return
			}
			buf := make([]byte, 1500)
			conn.SetReadDeadline(time.Now().Add(time.Second))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				results <- result{server, ""}
				return
			}
			res := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
			if err = res.Decode(); err != nil {
				results <- result{server, ""}
				return
			}
			var x stun.XORMappedAddress
			if err = x.GetFrom(res); err != nil {
				results <- result{server, ""}
				return
			}
			results <- result{server, x.String()}
		}(server)
	}
	var mappings []string
	for range stunServers {
		if r := <-results; r.mapping != "" {
			mappings = append(mappings, r.mapping)
		}
	}
	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})
	return mappings
}

// probeNAT classifies the local NAT with a throwaway socket.
func probeNAT() string {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return "probe failed: " + err.Error()
	}
	defer conn.Close()
	return classifyNAT(probeConn(conn))
}

// classifyNAT interprets the mappings seen from a single socket.
func classifyNAT(maps []string) string {
	switch {
	case len(maps) == 0:
		return "unreachable (no STUN responses)"
	case len(maps) == 1:
		return fmt.Sprintf("single response: %s (add more STUN servers to classify)", maps[0])
	}
	for _, m := range maps[1:] {
		if m != maps[0] {
			return "endpoint-dependent (symmetric): " + strings.Join(maps, ", ")
		}
	}
	return "endpoint-independent (cone): " + maps[0]
}

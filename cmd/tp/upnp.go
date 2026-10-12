package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"
)

type igdClient interface {
	AddPortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string, internalPort uint16, internalClient string, enabled bool, desc string, lease uint32) error
	DeletePortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string) error
	GetExternalIPAddressCtx(ctx context.Context) (string, error)
}

func gatewayHost(c igdClient) string {
	var loc *url.URL
	switch v := c.(type) {
	case *internetgateway2.WANIPConnection1:
		loc = v.Location
	case *internetgateway2.WANIPConnection2:
		loc = v.Location
	}
	if loc == nil {
		return ""
	}
	return loc.Hostname()
}

// localAddrToward returns the local IPv4 used to reach host.
func localAddrToward(host string) string {
	conn, err := net.Dial("udp4", net.JoinHostPort(host, "1900"))
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// resolveEndpoints turns --upnp into n concrete public endpoints (mappings
// created, cleanup returned) or passes the single --public endpoint through.
func resolveEndpoints(public string, upnp bool, n int) ([]string, func()) {
	if public != "" {
		return []string{public}, nil
	}
	if !upnp || n <= 0 {
		return nil, nil
	}
	eps, ok, stop := upnpMapping(n)
	if ok {
		return eps, stop
	}
	fmt.Fprintln(os.Stderr, "tp: upnp: no IGD mapping — falling back to STUN hole punching")
	return nil, nil
}

// upnpMapping asks the router (UPnP IGD) for n UDP port-forwards.
// Returns the public endpoints, whether it succeeded, and a cleanup func.
func upnpMapping(n int) ([]string, bool, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var clients []igdClient
	if cs, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(ctx); err == nil {
		for _, c := range cs {
			clients = append(clients, c)
		}
	}
	if cs, _, err := internetgateway2.NewWANIPConnection1ClientsCtx(ctx); err == nil {
		for _, c := range cs {
			clients = append(clients, c)
		}
	}

	var endpoints []string
	var stops []func()
outer:
	for _, c := range clients {
		extIP, err := c.GetExternalIPAddressCtx(ctx)
		if err != nil {
			continue
		}
		ip := net.ParseIP(extIP)
		if ip == nil || ip.To4() == nil {
			continue
		}
		localIP := localAddrToward(gatewayHost(c))
		if localIP == "" {
			continue
		}
		for range 5 * n {
			if len(endpoints) == n {
				break outer
			}
			port := uint16(20000 + rand.IntN(40000))
			err := c.AddPortMappingCtx(ctx, "", port, "UDP", port, localIP, true, "tp", 3600)
			if err != nil {
				continue
			}
			fmt.Fprintf(os.Stderr, "tp: upnp: mapped %s:%d -> %s:%d (UDP, 1h lease)\n", extIP, port, localIP, port)
			done := make(chan struct{})
			go func() {
				t := time.NewTicker(30 * time.Minute)
				defer t.Stop()
				for {
					select {
					case <-done:
						return
					case <-t.C:
						rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
						_ = c.AddPortMappingCtx(rctx, "", port, "UDP", port, localIP, true, "tp", 3600)
						rcancel()
					}
				}
			}()
			stops = append(stops, func() {
				close(done)
				rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = c.DeletePortMappingCtx(rctx, "", port, "UDP")
				rcancel()
			})
			endpoints = append(endpoints, fmt.Sprintf("%s:%d", extIP, port))
		}
	}
	if len(endpoints) == 0 {
		return nil, false, nil
	}
	stop := func() {
		for _, s := range stops {
			s()
		}
		fmt.Fprintln(os.Stderr, "tp: upnp: mappings removed")
	}
	return endpoints, true, stop
}

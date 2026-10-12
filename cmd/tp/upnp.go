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

// resolveEndpoint turns --upnp into a concrete public endpoint (mapping created,
// cleanup returned) or passes --public through unchanged.
func resolveEndpoint(public string, upnp bool) (string, func()) {
	if public != "" || !upnp {
		return public, nil
	}
	if ep, ok, stop := upnpMapping(); ok {
		return ep, stop
	}
	fmt.Fprintln(os.Stderr, "tp: upnp: no IGD mapping — falling back to STUN hole punching")
	return "", nil
}

// upnpMapping asks the router (UPnP IGD) for a UDP port-forward.
// Returns the public endpoint, whether it succeeded, and a cleanup func.
func upnpMapping() (string, bool, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		for range 5 {
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
			stop := func() {
				close(done)
				rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = c.DeletePortMappingCtx(rctx, "", port, "UDP")
				rcancel()
				fmt.Fprintln(os.Stderr, "tp: upnp: mapping removed")
			}
			return fmt.Sprintf("%s:%d", extIP, port), true, stop
		}
	}
	return "", false, nil
}

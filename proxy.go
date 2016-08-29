package main

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

type proxy struct {
	ports     []string
	serverUrl *url.URL
	tcpIp     string
}

// "7777:7778,ws://localhost:8000,127.0.0.1"
func parseProxy(r string) ([]*proxy, error) {
	var ps []*proxy
	for _, raw := range strings.Split(r, "|") {
		parts := strings.Split(raw, ",")
		if len(parts) != 2 && len(parts) != 3 {
			return nil, fmt.Errorf("Every proxy MUST have 2-3 parts: %v", parts)
		}

		// part[0]
		p := proxy{ports: strings.Split(parts[0], ":")}

		// part[1]
		serverUrl, err := url.Parse(parts[1])
		if err != nil {
			return nil, err
		}
		serverUrl.Host = authorityAddr(serverUrl.Scheme, serverUrl.Host)
		p.serverUrl = serverUrl

		// part[2]
		if len(parts) == 3 {
			_, port, _ := net.SplitHostPort(serverUrl.Host)
			p.tcpIp = net.JoinHostPort(parts[2], port)
		}

		ps = append(ps, &p)
	}

	return ps, nil
}

func authorityAddr(scheme string, authority string) (addr string) {
	if _, _, err := net.SplitHostPort(authority); err == nil {
		return authority
	}
	port := "443"
	if scheme == "ws" || scheme == "http" {
		port = "80"
	}
	return net.JoinHostPort(authority, port)
}

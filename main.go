package main

import (
	"flag"
	"net"
	"os"
	"time"

	"golang.org/x/net/http2"

	log "github.com/Sirupsen/logrus"
	"github.com/empirefox/wsh2c/client"
	"github.com/gorilla/websocket"
)

const (
	bufSize = 32 << 10

	wsHandshakeTimeout time.Duration = 20 * time.Second

	defaultProxy = "7777,ws://127.0.0.1:9999"
)

var (
	p   = flag.String("p", defaultProxy, "proxy command")
	h2v = flag.Bool("h2v", false, "enable http2 verbose logs")
)

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.TextFormatter{})

	// Output to stderr instead of stdout, could also be a file.
	log.SetOutput(os.Stderr)

	// Only log the warning severity or above.
	log.SetLevel(log.InfoLevel)
}

func main() {
	flag.Parse()
	http2.VerboseLogs = *h2v

	ps, err := parseProxy(*p)
	if err != nil {
		log.Fatalf("invalid proxy command: %s", err)
	}

	c := make(chan struct{})
	for _, p := range ps {
		for _, port := range p.ports {
			go serveProxy(port, p, c)
		}
	}
	<-c
}

func serveProxy(port string, p *proxy, quit chan<- struct{}) {

	c := &client.Client{
		Port:       port,
		ServerUrl:  p.serverUrl,
		PingPeriod: time.Second * 40,
		Dialer: websocket.Dialer{
			ReadBufferSize:  bufSize,
			WriteBufferSize: bufSize,
		},
		BufSize: bufSize,
	}

	if p.tcpIp != "" {
		c.Dialer.NetDial = func(network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{Deadline: time.Now().Add(wsHandshakeTimeout)}
			return dialer.Dial(network, p.tcpIp)
		}
	} else {
		c.Dialer.HandshakeTimeout = wsHandshakeTimeout
	}

	log.Error(c.Run())
	quit <- struct{}{}
}

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
	bufSize = 65 << 10

	wsHandshakeTimeout time.Duration = 20 * time.Second

	defaultProxy = "7777,ws://127.0.0.1:9999"
)

var (
	p   = flag.String("p", defaultProxy, "proxy command")
	up  = flag.Bool("up", false, "update pac to server")
	h2v = flag.Bool("h2v", false, "enable http2 verbose logs")
)

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.TextFormatter{})

	// Output to stderr instead of stdout, could also be a file.
	log.SetOutput(os.Stderr)

	// Only log the warning severity or above.
	log.SetLevel(log.InfoLevel)

	client.SetLogLevel(log.InfoLevel)
}

func main() {
	flag.Parse()
	http2.VerboseLogs = *h2v

	pc := *p
	if pc == defaultProxy {
	}
	ps, err := parseProxy(pc)
	if err != nil {
		log.Fatalf("invalid proxy command: %s", err)
	}

	var clients []*client.Client
	for _, p := range ps {
		for _, port := range p.ports {
			clients = append(clients, newClient(port, p))
		}
	}

	pac, err := clients[0].FetchPac(*up)
	if err != nil {
		log.Fatalf("fetch pac: %s", err)
	}
	if *up {
		os.Exit(0)
	}

	quit := make(chan struct{})
	for _, c := range clients {
		c.PacTpl = pac
		go serveProxy(c, quit)
	}
	<-quit
}

func serveProxy(c *client.Client, quit chan struct{}) {
	log.Errorln(c.Run())
	quit <- struct{}{}
}

func newClient(port string, p *proxy) *client.Client {
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

	c.PreRun()

	return c
}

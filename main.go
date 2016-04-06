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
)

var (
	parentPort = flag.String("port", "3128", "proxy serve port")
	server     = flag.String("ws", "127.0.0.1:9999", "ws server as parent")
	h2v        = flag.Bool("h2v", false, "enable http2 verbose logs")
)

func init() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.TextFormatter{})

	// Output to stderr instead of stdout, could also be a file.
	log.SetOutput(os.Stderr)

	// Only log the warning severity or above.
	log.SetLevel(log.WarnLevel)
}

func main() {
	flag.Parse()
	serveProxy()
}

func serveProxy() {
	http2.VerboseLogs = *h2v

	c := &client.Client{
		Port:       *parentPort,
		Server:     authorityAddr(*server),
		PingPeriod: time.Second * 40,
		Dialer: websocket.Dialer{
			ReadBufferSize:  bufSize,
			WriteBufferSize: bufSize,
		},
		BufSize: bufSize,
	}
	log.Error(c.Run())
}

func authorityAddr(authority string) (addr string) {
	if _, _, err := net.SplitHostPort(authority); err == nil {
		return authority
	}
	return net.JoinHostPort(authority, "80")
}

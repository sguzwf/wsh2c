package main

import (
	"flag"
	"net"
	"time"

	"golang.org/x/net/http2"

	"github.com/empirefox/wsh2c/client"
	"github.com/golang/glog"
	"github.com/gorilla/websocket"
)

const (
	bufSize = 32 << 10
)

var (
	parentPort = flag.String("port", "3128", "proxy serve port")
	server     = flag.String("ws", "p2.pppome.tk:8000", "ws server as parent")
	h2v        = flag.Bool("h2v", false, "enable http2 verbose logs")
)

func init() {
	//	flag.Set("stderrthreshold", "INFO")
	flag.Set("logtostderr", "true")
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
	glog.Fatalln(c.Run())
}

func authorityAddr(authority string) (addr string) {
	if _, _, err := net.SplitHostPort(authority); err == nil {
		return authority
	}
	return net.JoinHostPort(authority, "80")
}

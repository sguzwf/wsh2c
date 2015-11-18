package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/net/http2"

	"github.com/empirefox/cow"
	"github.com/empirefox/wsh2c/client"
	"github.com/golang/glog"
	"github.com/gorilla/websocket"
)

const (
	bufSize = 32 << 10
)

var (
	localPort  = flag.String("lp", "7777", "local proxy port")
	parentPort = flag.String("pp", "3128", "port to directly connect parent")
	server     = flag.String("ws", "ws://127.0.0.1:9999", "ws server as parent")
	pool       = flag.Int("ps", 10, "ws conn pool size")
	h2v        = flag.Bool("h2v", false, "enable http2 verbose logs")
)

//func init() {
//	flag.Set("stderrthreshold", "INFO")
//}

func main() {
	flag.Parse()
	parentOk := make(chan struct{})
	go servParent(parentOk)
	fmt.Println(">>>>>>>>>>>>>>> Starting init ws connection pool...")
	<-parentOk
	fmt.Println(">>>>>>>>>>>>>>> Proxy is on port:", *localPort)
	servLocal()
}

func servParent(parentOk chan<- struct{}) {
	http2.VerboseLogs = *h2v

	c := &client.Client{
		Port:       *parentPort,
		Server:     *server + "/h2p",
		PingPeriod: time.Second * 40,
		Dialer: websocket.Dialer{
			ReadBufferSize:  bufSize,
			WriteBufferSize: bufSize,
		},
		WsPoolSize: *pool,
		BufSize:    bufSize,
	}
	glog.Fatalln(c.Run(parentOk))
}

func servLocal() {
	parser := cow.ConfigParser()
	parser.ParseCore(strconv.Itoa(runtime.NumCPU()))
	parser.ParseProxy("http://127.0.0.1:" + *parentPort)
	parser.ParseListen("http://0.0.0.0:" + *localPort)
	cow.Start(func(config *cow.Config) {
		if err := os.MkdirAll(filepath.Dir(config.RcFile), os.ModePerm); err != nil {
			glog.Fatalln(err)
		}
		config.RcFile = ""
	})
}

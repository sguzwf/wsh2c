package client

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"

	"golang.org/x/net/http2"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/websocket"
)

const (
	h2FrameSize = 16 << 10
)

var (
	HOST         = "i:80"
	ErrWsInit    = errors.New("Cannot init ws!")
	PacReqPrefix = []byte("GET /pac HTTP/1.")
)

type Client struct {
	Port         string
	ServerUrl    *url.URL
	PingPeriod   time.Duration
	Dialer       websocket.Dialer
	BufSize      int
	h2Transport  http.RoundTripper
	h2ReverseReq http.Request
	pacTpl       *template.Template
}

func (client *Client) DialProxyTLS(network, addr string, cfg *tls.Config) (c net.Conn, err error) {
	if c, err = client.dialProxyTLS(network, addr, cfg); err != nil {
		log.WithFields(log.Fields{
			"server":     client.ServerUrl.Host,
			"tls.server": cfg.ServerName,
		}).Errorln(err)
	}
	return
}

func (client *Client) dialProxyTLS(network, addr string, cfg *tls.Config) (net.Conn, error) {
	ws, _, err := client.Dialer.Dial(client.ServerUrl.String()+"/h2p", nil)
	if err != nil {
		return nil, err
	}
	closeWs := ws
	defer func() {
		if closeWs != nil {
			closeWs.Close()
		}
	}()

	pc := NewWs(ws, client.BufSize, client.PingPeriod)
	u := url.URL{Host: client.ServerUrl.Host}
	_, hostNoPort := hostPortNoPort(&u)
	cfg.ServerName = hostNoPort
	cn := tls.Client(pc, cfg)

	if err := cn.Handshake(); err != nil {
		return nil, err
	}
	if !cfg.InsecureSkipVerify {
		if err := cn.VerifyHostname(cfg.ServerName); err != nil {
			return nil, err
		}
	}
	state := cn.ConnectionState()
	if p := state.NegotiatedProtocol; p != http2.NextProtoTLS {
		return nil, fmt.Errorf("http2: unexpected ALPN protocol %q; want %q", p, http2.NextProtoTLS)
	}
	if !state.NegotiatedProtocolIsMutual {
		return nil, errors.New("http2: could not negotiate protocol mutually")
	}
	closeWs = nil
	log.Infoln("DailTLS ok")
	go client.ping(ws)
	return cn, nil
}

func (client *Client) newH2Transport() http.RoundTripper {
	return &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: os.Getenv("TEST_MODE") == "1",
		},
		DialTLS: client.DialProxyTLS,
	}
}

func (client *Client) ping(ws *websocket.Conn) {
	ticker := time.NewTicker(client.PingPeriod)
	defer func() {
		ticker.Stop()
		ws.Close()
		log.Infoln("Ws closed")
	}()

	req := &http.Request{
		Method: "HEAD",
		URL: &url.URL{
			Scheme: "https",
			Host:   client.ServerUrl.Host,
			Path:   "/",
		},
		Host:       HOST,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}

	for {
		select {
		case <-ticker.C:
			res, err := client.h2Transport.RoundTrip(req)
			if err != nil || res.StatusCode != http.StatusOK {
				return
			}
		}
	}
}

func (client *Client) initWs() (err error) {
	if client.pacTpl != nil {
		return nil
	}

	cc, cs := net.Pipe()

	go func() {
		var resBuf bytes.Buffer
		resReader := bufio.NewReader(io.TeeReader(cc, &resBuf))
		if res, rerr := http.ReadResponse(resReader, nil); rerr != nil {
			err = rerr
		} else if res.StatusCode == http.StatusOK {
			defer res.Body.Close()
			if body, berr := ioutil.ReadAll(res.Body); berr != nil {
				err = berr
			} else {
				client.pacTpl = template.Must(template.New("pac").Parse(string(body)))
			}
		} else {
			log.Infoln(resBuf.String())
			err = ErrWsInit
		}
	}()

	go func() {
		rawReq := []byte(fmt.Sprintf("GET http://%s/ HTTP/1.1\r\nHost: %s\r\n\r\n", HOST, HOST))
		cc.Write(rawReq)
	}()

	client.connect(cs)
	return
}

func (client *Client) Run() error {
	client.initReverseRequest()
	client.h2Transport = client.newH2Transport()
	if err := client.initWs(); err != nil {
		return err
	}

	l, err := net.Listen("tcp", ":"+client.Port)
	if err != nil {
		return err
	}
	defer l.Close()
	fmt.Println(">>>>>>>>>>>>>>> OK proxy is on port", client.Port)
	fmt.Println(">>>>>>>>>>>>>>> Enjoy your life now!")

	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		c, e := l.Accept()
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				log.Infoln("http: Accept error: %v; retrying in %v\n", e, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return e
		}
		tempDelay = 0
		go client.connect(c)
	}
}

func (client *Client) initReverseRequest() {
	u := *client.ServerUrl
	//	u.Host => proxy
	u.Opaque = ""
	u.Scheme = "https"
	u.Path = "/r"
	client.h2ReverseReq = http.Request{
		Method:     "POST",
		URL:        &u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
}

// Extract target host, we only need that. The request is replayed to proxy server.
// The underline url is pointing to proxy server.
func (client *Client) newReverseRequest(requestURI string) (*http.Request, error) {
	reqUrl, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return nil, err
	}

	req := client.h2ReverseReq
	u := *req.URL
	req.URL = &u
	req.Header = make(http.Header)
	req.Host, _ = hostPortNoPort(reqUrl) // => authority|target
	return &req, nil
}

func (client *Client) Connect(c net.Conn) {}

// golang/x/net/http2
//
// @@ RoundTripOpt
// - addr := authorityAddr(req.URL.Host)
// + addr := req.RemoteAddr
//
// +++>
//	go cc.readLoop()
//	go cc.pingLoop()
//	return cc, nil
//}
//
//func (cc *ClientConn) pingLoop() {
//	var data [8]byte
//	ticker := time.NewTicker(time.Second * 30)
//	defer ticker.Stop()
//	for {
//		select {
//		case <-cc.readerDone:
//			logrus.Infoln("ClientConn readerDone")
//			return
//		case <-ticker.C:
//			sec := strconv.FormatInt(time.Now().Unix(), 36) + "__"
//			copy(data[:], sec[:8])
//			if err := cc.fr.WritePing(false, data); err != nil {
//				logrus.Errorln(err)
//				return
//			}
//		}
//	}
//}
//
// Another implements https://github.com/fangdingjun/net
// For now we choose smallest modification for original code
func (client *Client) connect(c net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			log.Errorln(err)
		}
	}()
	defer c.Close()

	bufConn := bufio.NewReader(c)
	requestLine, err := peekRequestLine(bufConn)
	if err != nil {
		log.Infoln(err)
		return
	}

	// pac
	if bytes.HasPrefix(requestLine, PacReqPrefix) {
		if err := client.pacTpl.Execute(c, c.LocalAddr().String()); err != nil {
			log.WithError(err).WithField("LocalAddr", c.LocalAddr().String()).Errorln("Exec pac")
		}
		return
	}

	// connect or reverse
	method, requestURI, _, ok := parseRequestLine(string(requestLine))
	if !ok {
		log.WithField("requestLine", requestLine).Infoln("malformed HTTP request")
		return
	}
	isConnect := method == "CONNECT"

	var req *http.Request
	if isConnect {
		if req, err = http.ReadRequest(bufConn); err != nil {
			log.Infoln(err)
			return
		}
		req.Host, _ = hostPortNoPort(req.URL) // => authority|target
	} else {
		if req, err = client.newReverseRequest(requestURI); err != nil {
			log.Infoln(err)
			return
		}
	}
	//	req.RemoteAddr = client.Server // This field is ignored by the HTTP client.
	req.URL.Scheme = "https"
	req.URL.Host = client.ServerUrl.Host
	req.ContentLength = -1
	if isConnect {
		req.Body = ioutil.NopCloser(bufConn)
	} else {
		reversePipeReader, reversePipeWriter := io.Pipe()
		req.Body = ioutil.NopCloser(bufio.NewReaderSize(reversePipeReader, h2FrameSize))
		go checkRequestEnd(reversePipeWriter, bufConn)
	}

	res, err := client.h2Transport.RoundTrip(req)
	if err != nil {
		log.WithError(err).WithField("res", res).Infoln("h2 RoundTrip")
		c.Write([]byte("HTTP/1.1 502 That's no street, Pete\r\n\r\n"))
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Write([]byte(fmt.Sprintf("HTTP/1.1 %d Server failed to proxy\r\n\r\n", res.StatusCode)))
		return
	}

	if isConnect {
		c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	}

	//	if isConnect {
	//		_, err = io.Copy(c, res.Body)
	//	} else {
	//		_, err = io.Copy(c, io.TeeReader(res.Body, os.Stdout))
	//	}
	_, err = io.Copy(c, res.Body)
	if err != nil {
		log.Infoln(err)
	}
}

func checkRequestEnd(w *io.PipeWriter, c io.Reader) {
	req, err := http.ReadRequest(bufio.NewReaderSize(io.TeeReader(c, w), h2FrameSize))
	if err != nil {
		w.CloseWithError(err)
		return
	}
	defer req.Body.Close()
	_, err = io.Copy(ioutil.Discard, req.Body)
	w.CloseWithError(err)
}

// from go request.go
// parseRequestLine parses "GET /foo HTTP/1.1" into its three parts.
func parseRequestLine(requestLine string) (method, requestURI, proto string, ok bool) {
	s1 := strings.Index(requestLine, " ")
	s2 := strings.Index(requestLine[s1+1:], " ")
	if s1 < 0 || s2 < 0 {
		return
	}
	s2 += s1 + 1
	return requestLine[:s1], requestLine[s1+1 : s2], requestLine[s2+1:], true
}

// convert from net/textproto/reader.go:Reader.upcomingHeaderNewlines
func peekRequestLine(r *bufio.Reader) ([]byte, error) {
	r.Peek(1) // force a buffer load if empty
	s := r.Buffered()
	if s == 0 {
		return nil, errors.New("No request in connection")
	}
	peek, _ := r.Peek(s)
	line, err := bytes.NewBuffer(peek).ReadBytes('\n')
	if err != nil {
		return nil, errors.New("Bad request in connection")
	}
	return line, nil
}

// from gorilla
func hostPortNoPort(u *url.URL) (hostPort, hostNoPort string) {
	hostPort = u.Host
	hostNoPort = u.Host
	if i := strings.LastIndex(u.Host, ":"); i > strings.LastIndex(u.Host, "]") {
		hostNoPort = hostNoPort[:i]
	} else {
		if u.Scheme == "wss" || u.Scheme == "https" {
			hostPort += ":443"
		} else {
			hostPort += ":80"
		}
	}
	return hostPort, hostNoPort
}

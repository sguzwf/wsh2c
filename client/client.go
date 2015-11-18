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
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/golang/glog"
	"github.com/gorilla/websocket"
)

const (
	h2FrameSize = 16 << 10
)

var (
	HOST          = "i:80"
	ErrWsPoolSize = errors.New("WsPoolSize must be set")
	ErrWsInit     = errors.New("Cannot init even one ws!")
)

type Client struct {
	Port         string
	Server       string
	PingPeriod   time.Duration
	Dialer       websocket.Dialer
	WsPoolSize   int
	BufSize      int
	h2Transport  http.RoundTripper
	h2ReverseReq http.Request
	seqmu        sync.Mutex
	seq          int
}

func (client *Client) DialProxyTLS(network, addr string, cfg *tls.Config) (net.Conn, error) {
	ws, _, err := client.Dialer.Dial(client.Server, nil)
	if err != nil {
		glog.Errorln(err)
		return nil, err
	}
	glog.Infoln("lower websocket ready")
	closeWs := ws
	defer func() {
		if closeWs != nil {
			closeWs.Close()
		}
	}()

	pc := NewWs(ws, client.BufSize, client.PingPeriod)
	u, _ := parseURL(client.Server)
	_, hostNoPort := hostPortNoPort(u)
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
	glog.Infoln("DailTLS ok")
	return cn, nil
}

func (client *Client) newH2Transport() http.RoundTripper {
	return &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DialTLS: client.DialProxyTLS,
	}
}

func (client *Client) initWsPool() error {
	if client.WsPoolSize == 0 {
		return ErrWsPoolSize
	}
	rawReq := []byte(fmt.Sprintf("GET http://%s/ HTTP/1.1\r\nHost: %s\r\n\r\n", HOST, HOST))
	var success int
	for i := 0; i < client.WsPoolSize+2; i++ {
		cc, cs := net.Pipe()
		go func() {
			var resBuf bytes.Buffer
			resReader := bufio.NewReader(io.TeeReader(cc, &resBuf))
			if res, err := http.ReadResponse(resReader, nil); err != nil {
				glog.Errorln("Init Ws pool err:", err)
			} else if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusFound {
				glog.Infoln(res.StatusCode)
				success++
			} else {
				glog.Infoln(resBuf.String())
			}
		}()
		go func() {
			cc.Write(rawReq)
		}()
		client.connect(cs)
	}
	if success == 0 {
		return ErrWsInit
	}
	return nil
}

func (client *Client) Run(ok chan<- struct{}) error {
	if err := client.initReverseRequest(); err != nil {
		return err
	}
	client.h2Transport = client.newH2Transport()
	if err := client.initWsPool(); err != nil {
		return err
	}

	l, err := net.Listen("tcp", ":"+client.Port)
	if err != nil {
		return err
	}
	defer l.Close()
	fmt.Println(">>>>>>>>>>>>>>> OK our parent is on port", client.Port)
	fmt.Println(">>>>>>>>>>>>>>> Enjoy your life now!")
	ok <- struct{}{}

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
				glog.Infoln("http: Accept error: %v; retrying in %v\n", e, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return e
		}
		tempDelay = 0
		go client.connect(c)
	}
}

func (client *Client) initReverseRequest() error {
	u, err := url.Parse(client.Server)
	if err != nil {
		return err
	}
	u.Scheme = "https"
	u.Path = "/r"
	client.h2ReverseReq = http.Request{
		Method:     "POST",
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Host:       u.Host,
	}
	return nil
}

func (client *Client) newReverseRequest(requestURI string) (*http.Request, error) {
	reqUrl, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return nil, err
	}

	req := client.h2ReverseReq
	u := *req.URL
	req.URL = &u
	req.Header = make(http.Header)
	req.Host, _ = hostPortNoPort(reqUrl)
	return &req, nil
}

func (client *Client) nextAddr() string {
	client.seqmu.Lock()
	defer client.seqmu.Unlock()
	client.seq++
	client.seq %= client.WsPoolSize
	return fmt.Sprintf("wsh2.addr:%d", client.seq)
}

// golang/x/net/http2
//
// maxConcurrentStreams = 1
//
// @@ RoundTripOpt
// - addr := authorityAddr(req.URL.Host)
// + addr := req.RemoteAddr
//
// @@ func (cc *ClientConn) RoundTrip
// - if gotResHeaders != nil {
// + if gotResHeaders != nil && !(req.Method == "CONNECT" && re.res.StatusCode == http.StatusOK) {
//
// @@ writeRequestBody
// - n, err := io.ReadFull(body, buf)
// + n, err := body.Read(buf)
//
//   err = cc.fr.WriteData(cs.ID, sentEnd, data)
// + cc.bw.Flush()
//
// +++>
//	go cc.readLoop()
//	go cc.pingLoop()
//	return cc, nil
//}
//
//func (cc *ClientConn) pingLoop() {
//	var data [8]byte
//	ticker := time.NewTicker(time.Second * 48)
//	defer ticker.Stop()
//	for {
//		select {
//		case <-cc.readerDone:
//			glog.Infoln("ClientConn readerDone")
//			return
//		case <-ticker.C:
//			sec := strconv.FormatInt(time.Now().Unix(), 36) + "__"
//			copy(data[:], sec[:8])
//			if err := cc.writePing(data); err != nil {
//				glog.Errorln(err)
//				return
//			}
//		}
//	}
//}
//
//func (cc *ClientConn) writePing(data [8]byte) error {
//	cc.wmu.Lock()
//	defer cc.wmu.Unlock()
//	if err := cc.fr.WritePing(false, data); err != nil {
//		return err
//	}
//	return cc.bw.Flush()
//}
// +++<
//
// Another implements https://github.com/fangdingjun/net
// For now we choose smallest modification for original code
func (client *Client) connect(c net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			glog.Errorln(err)
		}
		glog.Infoln("proxy conn closed")
	}()
	defer c.Close()

	bufConn := bufio.NewReader(c)
	requestLine, err := peekRequestLine(bufConn)
	if err != nil {
		glog.Errorln(err)
		return
	}
	method, requestURI, _, ok := parseRequestLine(string(requestLine))
	if !ok {
		glog.Errorf("malformed HTTP request: %s\n", requestLine)
		return
	}
	isConnect := method == "CONNECT"

	var req *http.Request
	if isConnect {
		if req, err = http.ReadRequest(bufConn); err != nil {
			return
		}
		req.Host, _ = hostPortNoPort(req.URL)
		req.URL.Opaque = "/c"
	} else {
		if req, err = client.newReverseRequest(requestURI); err != nil {
			glog.Errorln(err)
			return
		}
	}
	req.RemoteAddr = client.nextAddr()
	req.URL.Scheme = "https"
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
		glog.Errorln(err)
		c.Write([]byte("HTTP/1.1 502 That's no street, Pete\r\n\r\n"))
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Write([]byte(fmt.Sprintf("HTTP/1.1 %d Server failed to proxy\r\n\r\n", res.StatusCode)))
		return
	}

	if isConnect {
		if _, err = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			glog.Errorln(err)
			return
		}
	}

	//	if isConnect {
	//		_, err = io.Copy(c, res.Body)
	//	} else {
	//		_, err = io.Copy(c, io.TeeReader(res.Body, os.Stdout))
	//	}
	_, err = io.Copy(c, res.Body)
	if err != nil {
		glog.Errorln(err)
	}
}

func checkRequestEnd(w *io.PipeWriter, c io.Reader) {
	req, err := http.ReadRequest(bufio.NewReaderSize(io.TeeReader(c, w), h2FrameSize))
	if err != nil {
		glog.Errorln(err)
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
var errMalformedURL = errors.New("malformed ws or wss URL")

// parseURL parses the URL. The url.Parse function is not used here because
// url.Parse mangles the path.
func parseURL(s string) (*url.URL, error) {
	// From the RFC:
	//
	// ws-URI = "ws:" "//" host [ ":" port ] path [ "?" query ]
	// wss-URI = "wss:" "//" host [ ":" port ] path [ "?" query ]
	//
	// We don't use the net/url parser here because the dialer interface does
	// not provide a way for applications to work around percent deocding in
	// the net/url parser.

	var u url.URL
	switch {
	case strings.HasPrefix(s, "ws://"):
		u.Scheme = "ws"
		s = s[len("ws://"):]
	case strings.HasPrefix(s, "wss://"):
		u.Scheme = "wss"
		s = s[len("wss://"):]
	default:
		return nil, errMalformedURL
	}

	u.Host = s
	u.Opaque = "/"
	if i := strings.Index(s, "/"); i >= 0 {
		u.Host = s[:i]
		u.Opaque = s[i:]
	}

	if strings.Contains(u.Host, "@") {
		// WebSocket URIs do not contain user information.
		return nil, errMalformedURL
	}

	return &u, nil
}

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

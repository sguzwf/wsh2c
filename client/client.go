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
	"text/template"
	"time"

	"golang.org/x/net/http2"

	"github.com/golang/glog"
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
	Server       string
	PingPeriod   time.Duration
	Dialer       websocket.Dialer
	BufSize      int
	h2Transport  http.RoundTripper
	h2ReverseReq http.Request
	pacTpl       *template.Template
}

func (client *Client) DialProxyTLS(network, addr string, cfg *tls.Config) (net.Conn, error) {
	ws, _, err := client.Dialer.Dial(fmt.Sprintf("ws://%s/h2p", client.Server), nil)
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
	u := url.URL{Host: client.Server}
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

func (client *Client) initWs() (err error) {
	cc, cs := net.Pipe()

	go func() {
		var resBuf bytes.Buffer
		resReader := bufio.NewReader(io.TeeReader(cc, &resBuf))
		if res, rerr := http.ReadResponse(resReader, nil); rerr != nil {
			glog.Errorln("Init Ws err:", rerr)
			err = rerr
		} else if res.StatusCode == http.StatusOK {
			defer res.Body.Close()
			if body, berr := ioutil.ReadAll(res.Body); berr != nil {
				err = berr
			} else {
				client.pacTpl = template.Must(template.New("pac").Parse(string(body)))
			}
		} else {
			glog.Infoln(resBuf.String())
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
	if err := client.initReverseRequest(); err != nil {
		return err
	}
	client.h2Transport = client.newH2Transport()
	if err := client.initWs(); err != nil {
		return err
	}

	l, err := net.Listen("tcp", ":"+client.Port)
	if err != nil {
		return err
	}
	defer l.Close()
	fmt.Println(">>>>>>>>>>>>>>> OK our proxy is on port", client.Port)
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
	u.Opaque = ""
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

// golang/x/net/http2
//
// @@ RoundTripOpt
// - addr := authorityAddr(req.URL.Host)
// + addr := req.RemoteAddr
//
// Another implements https://github.com/fangdingjun/net
// For now we choose smallest modification for original code
func (client *Client) connect(c net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			glog.Errorln(err)
		}
		//		glog.Infoln("proxy conn closed")
	}()
	defer c.Close()

	bufConn := bufio.NewReader(c)
	requestLine, err := peekRequestLine(bufConn)
	if err != nil {
		glog.Errorln(err)
		return
	}

	// pac
	if bytes.HasPrefix(requestLine, PacReqPrefix) {
		if err := client.pacTpl.Execute(c, c.LocalAddr().String()); err != nil {
			glog.Errorf("cannot exec tpl with addr: %s\n", c.LocalAddr().String())
		}
		return
	}

	// connect or reverse
	method, requestURI, _, ok := parseRequestLine(string(requestLine))
	if !ok {
		glog.Errorf("malformed HTTP request: %s\n", requestLine)
		return
	}
	isConnect := method == "CONNECT"

	var req *http.Request
	if isConnect {
		if req, err = http.ReadRequest(bufConn); err != nil {
			glog.Errorln(err)
			return
		}
		req.Host, _ = hostPortNoPort(req.URL)
	} else {
		if req, err = client.newReverseRequest(requestURI); err != nil {
			glog.Errorln(err)
			return
		}
	}
	req.RemoteAddr = client.Server // This field is ignored by the HTTP client.
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

package client

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/websocket"

	"golang.org/x/net/http2"
)

func (client *Client) DialProxyTLS(network, addr string, cfg *tls.Config) (c net.Conn, err error) {
	if c, err = client.dialProxyTLS(network, addr, cfg); err != nil {
		log.WithFields(logrus.Fields{
			"server": client.ServerUrl.Host,
		}).Errorln(err)
	}
	return
}

func (client *Client) dialProxyTLS(network, addr string, cfg *tls.Config) (net.Conn, error) {
	ws, _, err := client.Dialer.Dial(client.ServerUrl.String()+"/p", nil)
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

func (client *Client) ping(ws *websocket.Conn) {
	info, err := client.getServerInfo()
	if err != nil {
		log.Errorln("getServerInfo", err)
		return
	}

	ticker := time.NewTicker(info.PingSecond * time.Second)
	defer func() {
		ticker.Stop()
		ws.Close()
		log.Infoln("Ws closed")
	}()

	req := client.innerRequest("HEAD", HOST_OK)

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

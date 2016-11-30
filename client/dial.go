package client

import (
	"context"
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
			"port":   client.Port,
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
	go client.ping(ws, addr)
	return cn, nil
}

func (client *Client) ping(ws *websocket.Conn, addr string) {
	log.WithField("port", client.Port).Infoln("DailTLS ok: " + addr)
	info, err := client.getServerInfo()
	if err != nil {
		log.WithField("port", client.Port).Errorln("getServerInfo", err)
		return
	}

	ticker := time.NewTicker(info.PingSecond * time.Second)
	defer func() {
		ticker.Stop()
		ws.Close()
		log.WithField("port", client.Port).Infoln("Ws closed")
	}()
	log.WithField("port", client.Port).Infoln("Ws started")

	req := client.innerRequest("HEAD", HOST_OK)

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			res, err := client.h2Transport.RoundTrip(req.WithContext(ctx))
			if err != nil || res.StatusCode != http.StatusOK {
				cancel()
				return
			}
			cancel()
		}
	}
}

package client

import (
	"net"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/gorilla/websocket"
)

// chanFromConn creates a channel from a Conn object, and sends everything it
//  Read()s from the socket to the channel.
func chanFromConn(conn net.Conn, bufSize int) chan []byte {
	c := make(chan []byte)

	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Errorln(err)
			}
		}()

		b := make([]byte, bufSize)
		for {
			n, err := conn.Read(b)
			if n > 0 {
				c <- b[:n]
			}
			if err != nil {
				c <- nil
				break
			}
		}
	}()

	return c
}

func chanFromWs(ws *websocket.Conn) chan []byte {
	c := make(chan []byte)

	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Errorln(err)
			}
		}()

		for {
			_, b, err := ws.ReadMessage()
			if len(b) > 0 {
				c <- b
			}
			if err != nil {
				c <- nil
				break
			}
		}
	}()

	return c
}

// Pipe creates a full-duplex pipe between the two sockets and transfers data from one to the other.
func (client *Client) pipe(conn net.Conn, ws *websocket.Conn) {
	cc := chanFromConn(conn, client.BufSize)
	cw := chanFromWs(ws)
	ticker := time.NewTicker(client.PingPeriod)

	defer func() {
		ticker.Stop()
		if err := recover(); err != nil {
			log.WithField("err", err).Errorln("quit pipe")
		} else {
			log.Errorln("quit pipe")
		}
	}()

	//peer, err := strconv.ParseInt(msg, 10, 64)
	//period := strconv.FormatInt(time.Now().UnixNano()-peer, 10)
	for {
		select {
		case b := <-cc:
			if b == nil {
				return
			} else {
				// in write there is timeout set
				if err := ws.WriteMessage(websocket.BinaryMessage, b); err != nil {
					log.Infoln("write request error", err)
					return
				}
			}
		case b := <-cw:
			if b == nil {
				return
			} else {
				if _, err := conn.Write(b); err != nil {
					log.Infoln("write response error", err)
					return
				}
			}
		case <-ticker.C:
			if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				log.Infoln("ping error", err)
				return
			}
		}
	}
}

package client

import (
	"io"
	"strconv"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/websocket"
)

func NewWs(ws *websocket.Conn, bufSize int, pingPeriod time.Duration) *Ws {
	ws.SetPongHandler(func(msg string) error {
		sent, err := strconv.ParseInt(msg, 36, 64)
		if err != nil {
			glog.Warningln("Wrong pong time:", msg)
			return nil
		}
		glog.Infof("Ping time: %dns\n", time.Now().UnixNano()-sent)
		return nil
	})
	return &Ws{
		Conn:       ws,
		pingPeriod: pingPeriod,
		copyBuf:    make([]byte, bufSize),
	}
}

// Must use BinaryMessage type
type Ws struct {
	*websocket.Conn
	copyBuf       []byte
	reader        io.Reader
	pingPeriod    time.Duration
	OnTextMessage func(r io.Reader)
}

func (ws Ws) SetDeadline(t time.Time) error {
	if err := ws.SetReadDeadline(t); err != nil {
		return err
	}
	return ws.SetWriteDeadline(t)
}

func (ws Ws) WriteText(b []byte) error {
	return ws.Conn.WriteMessage(websocket.TextMessage, b)
}

func (ws Ws) Write(b []byte) (written int, err error) {
	err = ws.Conn.WriteMessage(websocket.BinaryMessage, b)
	//	glog.Infoln("ws sent", len(b))
	return len(b), err
}

func (ws *Ws) Read(p []byte) (n int, err error) {
	defer func() {
		//		glog.Infoln("ws resv", n)
	}()
	if ws.reader == nil {
		var t int
		var er error
		t, ws.reader, er = ws.NextReader()
		if er != nil {
			err = er
			return
		}
		if t != websocket.BinaryMessage {
			if ws.OnTextMessage != nil {
				ws.OnTextMessage(ws.reader)
			}
			ws.reader = nil
			n, err = ws.Read(p)
			return
		}
	}

	n, err = ws.reader.Read(p)
	if err == io.EOF {
		ws.reader = nil
		err = nil
		if n == 0 {
			n, err = ws.Read(p)
			return
		}
	}
	return
}

// Only used by io.Copy
func (ws Ws) ReadFrom(r io.Reader) (int64, error) {
	wc, err := ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return -1, err
	}
	defer wc.Close()
	return wc.(io.ReaderFrom).ReadFrom(r)
}

// Only used by io.Copy
func (ws *Ws) WriteTo(w io.Writer) (written int64, err error) {
	var n int64
	for {
		t, r, er := ws.NextReader()
		if er != nil {
			err = er
			break
		}
		if t != websocket.BinaryMessage {
			if ws.OnTextMessage != nil {
				ws.OnTextMessage(r)
			}
			continue
		}
		n, er = io.CopyBuffer(w, r, ws.copyBuf)
		written += n
		if er != nil {
			err = er
			break
		}
	}
	return written, err
}

// NOT compatable with funcs above
func (ws *Ws) Ping() {
	ticker := time.NewTicker(ws.pingPeriod)
	defer func() {
		glog.Infoln("ping ticker stop")
		ticker.Stop()
		ws.Close()
	}()

	for {
		select {
		case <-ticker.C:
			unixnano := strconv.FormatInt(time.Now().UnixNano(), 36)
			if err := ws.WriteMessage(websocket.PingMessage, []byte(unixnano)); err != nil {
				glog.Errorln(err)
				return
			}
		}
	}
}

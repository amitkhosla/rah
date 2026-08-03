package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn is a thin wrapper around gorilla/websocket.Conn that serializes writes.
// gorilla requires a single writer at a time; wmu enforces this invariant.
type wsConn struct {
	conn *websocket.Conn
	wmu  sync.Mutex // serialize writes
}

func newWSConn(c *websocket.Conn) *wsConn {
	return &wsConn{conn: c}
}

func (c *wsConn) ReadMessage() (messageType int, p []byte, err error) {
	return c.conn.ReadMessage()
}

func (c *wsConn) WriteMessage(messageType int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}

func (c *wsConn) WritePing(data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn.WriteMessage(websocket.PingMessage, data)
}

func (c *wsConn) WriteClose(code int, text string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	msg := websocket.FormatCloseMessage(code, text)
	return c.conn.WriteMessage(websocket.CloseMessage, msg)
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *wsConn) SetWriteDeadline(t time.Time) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn.SetWriteDeadline(t)
}

func (c *wsConn) SetPongHandler(h func(string) error) {
	c.conn.SetPongHandler(h)
}

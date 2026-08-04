package ws

import (
	"context"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const writeDeadline = 10 * time.Second

// RunOutbound is started as a goroutine for each session.
// It drains session.outCh and writes to the connection.
// Exits when ctx is cancelled, outCh is closed, or a close message is sent.
func RunOutbound(ctx context.Context, session *WSSession) {
	for {
		select {
		case msg, ok := <-session.outCh:
			if !ok {
				// Channel closed — connection is done.
				return
			}
			if msg.CloseCode != 0 {
				// Send a WebSocket close frame then stop.
				if err := session.conn.SetWriteDeadline(time.Now().Add(writeDeadline)); err != nil {
					log.Printf("ws: session %s set write deadline error: %v", session.id, err)
				}
				if err := session.conn.WriteClose(msg.CloseCode, msg.CloseText); err != nil {
					log.Printf("ws: session %s close write error: %v", session.id, err)
				}
				_ = session.conn.Close()
				return
			}
			// Data message.
			if err := session.conn.SetWriteDeadline(time.Now().Add(writeDeadline)); err != nil {
				log.Printf("ws: session %s set write deadline error: %v", session.id, err)
				return
			}
			if err := session.conn.WriteMessage(websocket.TextMessage, msg.Payload); err != nil {
				log.Printf("ws: session %s write error: %v", session.id, err)
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

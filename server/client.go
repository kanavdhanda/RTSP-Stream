package main

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// readPump handles incoming WebSocket messages from the client
func (c *Client) readPump() {
	defer func() {
		// Check if client is already closed to avoid double removal
		c.mu.Lock()
		alreadyClosed := c.closed
		c.mu.Unlock()

		if !alreadyClosed {
			c.manager.RemoveClient(c)
		}
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for client %s: %v", c.id, err)
			}
			break
		}
	}
}

// writePump handles outgoing frame data to the client via WebSocket
func (c *Client) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case frame, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Channel closed, send close message and exit
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Check if client is marked as closed before writing
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()

			if closed {
				return
			}

			// Send frame as binary data
			if err := c.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				log.Printf("Write error for client %s: %v", c.id, err)
				return
			}

		case <-ticker.C:
			// Check if client is marked as closed before sending ping
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()

			if closed {
				return
			}

			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

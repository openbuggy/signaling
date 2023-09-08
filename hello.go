package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"
)

type RtcMessage struct {
	Type    string          `json:"type"`
	From    string          `json:"from"`
	To      string          `json:"to"`
	Message json.RawMessage `json:"message"`
}

type PeersMessage struct {
	Type    string   `json:"type"`
	PeerIds []string `json:"peerIds"`
}

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Maximum message size allowed from peer.
	maxMessageSize = 1024 * 1024
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

type Client struct {
	id      string
	clients *Clients
	conn    *websocket.Conn
	send    chan []byte
}

func (c *Client) readPump() {
	defer func() {
		c.clients.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		data = bytes.TrimSpace(bytes.Replace(data, newline, space, -1))
		var message RtcMessage
		_ = json.Unmarshal(data, &message)
		message.From = c.id
		message.Type = "rtc"
		messageBytes, _ := json.Marshal(message)
		c.clients.clients[message.To].send <- messageBytes
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

type Clients struct {
	clients    map[string]*Client
	register   chan *Client
	unregister chan *Client
}

func newClients() *Clients {
	return &Clients{
		clients:    make(map[string]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Clients) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client.id] = client
		case client := <-h.unregister:
			if _, ok := h.clients[client.id]; ok {
				delete(h.clients, client.id)
				close(client.send)
			}
		}
		for id, client := range h.clients {
			peerIds := make([]string, len(h.clients)-1)
			i := 0
			for peerId := range h.clients {
				if id != peerId {
					peerIds[i] = peerId
					i++
				}
			}
			json, _ := json.Marshal(PeersMessage{Type: "peers", PeerIds: peerIds})
			client.send <- json
		}
	}
}

func connect(clients *Clients, w http.ResponseWriter, r *http.Request) {
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{id: uuid.NewString(), clients: clients, conn: conn, send: make(chan []byte, 256)}
	clients.register <- client

	go client.writePump()
	go client.readPump()
}

func main() {
	clients := newClients()
	go clients.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/connect", func(w http.ResponseWriter, r *http.Request) {
		connect(clients, w, r)
	})

	log.Fatal(http.ListenAndServe(":8080", cors.Default().Handler(mux)))
}

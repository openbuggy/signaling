package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

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
		log.Printf("cleanup client %s", c.id)
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, data, err := c.conn.ReadMessage()
		log.Printf("client %s read %s", c.id, string(data))
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("client %s read error %v", c.id, err)
			}
			break
		}
		var message RtcMessage
		err = json.Unmarshal(data, &message)
		if err != nil {
			log.Fatalf("unmarshal error %v", err)
			break
		}
		message.From = c.id
		message.Type = "rtc"
		messageBytes, err := json.Marshal(message)
		if err != nil {
			log.Fatalf("marshal error %v", err)
			break
		}
		log.Printf("send %s to %s", string(messageBytes), message.To)
		c.clients.clients[message.To].send <- messageBytes
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		log.Printf("client %s cleanup writepump", c.id)
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
				log.Fatalf("error opening writer %v", err)
				return
			}
			log.Printf("client %s write %s", c.id, string(message))
			w.Write(message)

			if err := w.Close(); err != nil {
				log.Printf("error closing writer %v", err)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			log.Printf("client %s write ping", c.id)
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("error writing ping %v", err)
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
			log.Printf("register %s", client.id)
			if prevClient, ok := h.clients[client.id]; ok {
				close(prevClient.send)
			}
			h.clients[client.id] = client
		case client := <-h.unregister:
			log.Printf("unregister %s", client.id)
			if _, ok := h.clients[client.id]; ok && h.clients[client.id] == client {
				delete(h.clients, client.id)
				close(client.send)
			}
		}
		log.Print("send peers to clients")
		for id, client := range h.clients {
			peerIds := make([]string, len(h.clients)-1)
			i := 0
			for peerId := range h.clients {
				if id != peerId {
					peerIds[i] = peerId
					i++
				}
			}
			json, err := json.Marshal(PeersMessage{Type: "peers", PeerIds: peerIds})
			if err != nil {
				log.Fatalf("error marshal peers %v", err)
			}
			client.send <- json
		}
	}
}

func connect(clients *Clients, w http.ResponseWriter, r *http.Request) {
	log.Print("connect new client")
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatalf("error upgrading connection %v", err)
		return
	}
	client := &Client{id: r.URL.Query().Get("id"), clients: clients, conn: conn, send: make(chan []byte, 256)}
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

	var port string
	if value, ok := os.LookupEnv("PORT"); ok {
		port = value
	} else {
		port = "5001"
	}

	log.Fatal(http.ListenAndServe(":"+port, cors.Default().Handler(mux)))
}

package main

import (
	"fmt"
	"log"
	"net/url"

	"github.com/gorilla/websocket"
)

func main() {
	u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/controller"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	go func() {
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("recv: %s", string(message))
		}
	}()

	for {
		var id string
		fmt.Scan(&id)
		//c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("{\"to\":\"%s\", \"data\": {\"test\": 1}}", id)))
	}
}

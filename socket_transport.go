package gophoenix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 45 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

type socketTransport struct {
	socket *websocket.Conn
	cr     ConnectionReceiver
	mr     MessageReceiver
	close  chan struct{}
	done   chan struct{}
	send   chan *Message
}

func (st *socketTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	st.mr = mr
	st.cr = cr

	// TODO Add origin header, handle resp from dial
	conn, _, err := websocket.DefaultDialer.Dial(url.String(), header)

	if err != nil {
		return err
	}

	st.socket = conn
	fmt.Println("Start goroutine")
	go st.writer()
	go st.listen()

	time.Sleep(1 * time.Second)

	st.cr.NotifyConnect()
	return err
}

func (st *socketTransport) Push(data *Message) error {
	fmt.Println("Send data")
	st.send <- data
	return nil
}

func (st *socketTransport) Close() {
	st.close <- struct{}{}
	<-st.done
}

func (st *socketTransport) writer() {
	fmt.Println("Init heartbeat", pingPeriod.String())
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		fmt.Println("stop - writer")
		ticker.Stop()
		st.stop()
	}()
	for {
		fmt.Println("into for write")
		select {
		case message := <-st.send:
			fmt.Println("writer message")
			b, _ := json.Marshal(message)

			if b != nil {
				fmt.Println("WriteJSON:", string(b))
			} else {
				fmt.Println("WriteJSON:", message)
			}

			if err := st.socket.WriteJSON(message); err != nil {
				fmt.Println("WriteJSON error:", err.Error())
				return
			}
		case <-ticker.C:
			fmt.Println("Send heartbeat")
			if err := st.Push(&Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				fmt.Println("Push Heartbeat error:", err.Error())
				return
			}
		}
	}
}

func (st *socketTransport) listen() {
	defer func() {
		fmt.Println("stop - listen")
		st.stop()
	}()
	st.socket.SetReadLimit(maxMessageSize)
	// st.socket.SetReadDeadline(time.Now().Add(pongWait))
	// st.socket.SetPongHandler(func(string) error {
	// 	st.socket.SetReadDeadline(time.Now().Add(pongWait))
	// 	fmt.Println("Send heartbeat")
	// if err := st.Push(Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
	// 	return err
	// }
	// 	return nil
	// })

	for {
		fmt.Println("into for listen")
		fmt.Println("Check Message")
		// var msg *Message
		_, p, err := st.socket.ReadMessage()
		if err != nil {
			fmt.Println("Error ReadJSON:", err.Error())
			continue
		}
		fmt.Println("Go a message", string(p))
		// if err := st.socket.ReadJSON(msg); err != nil {
		// 	fmt.Println("Error ReadJSON:", err.Error())
		// 	continue
		// }
		// fmt.Println("Go a message")
		//
		// b, _ := json.Marshal(msg)
		// fmt.Println("Income Message:", string(b))
		// st.mr.NotifyMessage(msg)
	}
}

func (st *socketTransport) stop() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	func() { st.done <- struct{}{} }()
}

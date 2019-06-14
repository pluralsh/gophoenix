package gophoenix

import (
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
	send   chan interface{}
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
	go st.start()

	st.cr.NotifyConnect()

	return err
}

func (st *socketTransport) start() {
	st.writer()
	st.listen()
}

func (st *socketTransport) Push(data interface{}) error {
	st.send <- data
	return nil
}

func (st *socketTransport) Close() {
	st.close <- struct{}{}
	<-st.done
}

func (st *socketTransport) writer() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		st.stop()
	}()
	for {
		select {
		case message := <-st.send:
			if err := st.socket.WriteJSON(message); err != nil {
				fmt.Println("WriteJSON error:", err.Error())
				return
			}
		case <-ticker.C:
			if err := st.Push(Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				fmt.Println("Push Heartbeat error:", err.Error())
				return
			}
		}
	}
}

func (st *socketTransport) listen() {
	fmt.Println("Init heartbeat", pingPeriod.String())
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
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
		// st.socket.SetWriteDeadline(time.Now().Add(writeWait))
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

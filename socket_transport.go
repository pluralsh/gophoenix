package gophoenix

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const (
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
}

func (st *socketTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	st.mr = mr
	st.cr = cr

	conn, _, err := websocket.DefaultDialer.Dial(url.String(), header)

	if err != nil {
		return err
	}

	st.socket = conn
	fmt.Println("Start goroutine")
	go st.writer()
	go st.listen()

	st.cr.NotifyConnect()
	return err
}

func (st *socketTransport) Push(data *Message) error {
	fmt.Println("Send data:", data)
	if err := st.socket.WriteJSON(data); err != nil {
		fmt.Println("WriteJSON error:", err.Error())
		return err
	}
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
		select {
		case <-ticker.C:
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

	for {
		var msg Message
		// _, p, err := st.socket.ReadMessage()
		// if err != nil {
		// 	fmt.Println("Error ReadJSON:", err.Error())
		// 	continue
		// }
		// fmt.Println("Got a message", string(p))
		if err := st.socket.ReadJSON(&msg); err != nil {
			fmt.Println("Error ReadJSON:", err.Error())
			continue
		}

		st.mr.NotifyMessage(&msg)
	}
}

func (st *socketTransport) stop() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	func() { st.done <- struct{}{} }()
}

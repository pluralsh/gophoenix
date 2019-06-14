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
	// Time allowed to read the next pong message from the peer.
	pongWait = 45 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
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

	// TODO Add origin header, handle resp from dial
	conn, _, err := websocket.DefaultDialer.Dial(url.String(), header)

	if err != nil {
		return err
	}

	st.socket = conn
	go st.listen()
	st.cr.NotifyConnect()

	return err
}

func (st *socketTransport) Push(data interface{}) error {
	return st.socket.WriteJSON(data)
}

func (st *socketTransport) Close() {
	st.close <- struct{}{}
	<-st.done
}

func (st *socketTransport) listen() {
	fmt.Println("Init heartbeat", pingPeriod.String())
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		st.stop()
	}()

	for {
		select {
		case <-ticker.C:
			fmt.Println("Send heartbeat")
			if err := st.Push(Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				return
			}
			continue
		case <-st.close:
			fmt.Println("Socket Closed")
			return
		}

		var msg *Message
		if err := st.socket.ReadJSON(msg); err != nil {
			fmt.Println("Error ReadJSON:", err.Error())
			continue
		}

		b, _ := json.Marshal(msg)
		fmt.Println("Income Message:", string(b))
		st.mr.NotifyMessage(msg)
	}
}

func (st *socketTransport) stop() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	func() { st.done <- struct{}{} }()
}

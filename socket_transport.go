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
	pongWait = 5 * time.Second

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
			fmt.Println("<-ticker.C")
			// st.socket.SetWriteDeadline(time.Now().Add(writeWait))
			fmt.Println("Send heartbeat")
			if err := st.Push(Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				// if err := st.socket.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-st.close:
			fmt.Println("<-st.close:")
			return
		default:
			fmt.Println("<-default:")
			// st.socket.SetWriteDeadline(time.Now().Add(writeWait))
			var msg *Message
			err := st.socket.ReadJSON(msg)

			if err != nil {
				continue
			}
			fmt.Println("msg")
			st.mr.NotifyMessage(msg)
		}
	}
}

func (st *socketTransport) stop() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	func() { st.done <- struct{}{} }()
}

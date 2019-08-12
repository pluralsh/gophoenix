package gophoenix

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
)

const (
	// Time allowed to read the next pong message from the peer.
	pongWait = 45 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1 << 20
)

type socketTransport struct {
	socket       *websocket.Conn
	dialer       *websocket.Dialer
	cr           ConnectionReceiver
	mr           MessageReceiver
	close        chan struct{}
	done         chan struct{}
	isConnected  bool
	isConnecting bool
	mu           sync.RWMutex
	url          url.URL
	header       http.Header
}

func (st *socketTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	st.mr = mr
	st.cr = cr
	st.url = url
	st.header = header

	err := st.dial()
	if err != nil {
		return err
	}

	st.setIsConnected(true)
	fmt.Println("Start goroutine")
	go st.writer()
	go st.listen()

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
				st.closeAndReconnect()
				return
			}
		}
	}
}

func (st *socketTransport) listen() {
	defer func() {
		st.stop()
	}()
	st.socket.SetReadLimit(maxMessageSize)

	for {
		var msg Message
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
	st.setIsConnected(false)
	func() { st.done <- struct{}{} }()
}

func (st *socketTransport) dial() error {
	conn, _, err := st.dialer.Dial(st.url.String(), st.header)

	if err != nil {
		return err
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.socket = conn

	return err
}

func (st *socketTransport) closeAndReconnect() {
	st.stop()
	go st.connect()
}

func (st *socketTransport) connect() {
	b := getBackoff()
	rand.Seed(time.Now().UTC().UnixNano())
	st.setIsConnected(false)
	st.setIsConnecting(true)
	defer st.setIsConnecting(false)

	for {
		nextItvl := b.Duration()
		err := st.dial()

		if err != nil {
			fmt.Println(err)
			fmt.Println("Error connecting - will try again in", nextItvl, "seconds.")
			time.Sleep(nextItvl)
		}

		fmt.Printf("Dial: connection was successfully established with %s\n", st.url.String())
		st.setIsConnected(true)
		return
	}
}

func (st *socketTransport) setIsConnected(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isConnected = state
	st.cr.NotifyConnect()
}

func (st *socketTransport) setIsConnecting(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isConnecting = state
}

func getBackoff() backoff.Backoff {
	return backoff.Backoff{
		Min:    5 * time.Second,
		Max:    30 * time.Second,
		Factor: 2,
		Jitter: true,
	}
}

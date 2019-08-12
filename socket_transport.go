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
	isListening  bool
	isWriting    bool
	mu           sync.RWMutex
	url          url.URL
	header       http.Header
}

func (st *socketTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	st.mr = mr
	st.cr = cr
	st.url = url
	st.header = header

	return st.connect(true)
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
	if st.getIsWriting() {
		return
	}
	st.setIsWriting(true)

	fmt.Println("Starting writer heartbeat: ", pingPeriod.String())
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		fmt.Println("Stopping writer...")
		ticker.Stop()
		st.stop()
	}()
	for {
		select {
		case <-ticker.C:
			if err := st.Push(&Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				fmt.Println("Error sending heartbeat:", err.Error())
				st.closeAndReconnect()
				return
			}
		}
	}
}

func (st *socketTransport) listen() {
	if st.getIsListening() {
		return
	}
	st.setIsListening(true)

	defer func() {
		st.closeAndReconnect()
	}()

	st.socket.SetReadLimit(maxMessageSize)

	for {
		var msg Message
		if err := st.socket.ReadJSON(&msg); err != nil {
			fmt.Println("Error reading from socket.  Retrying connection: ", err)
			return
		}

		st.mr.NotifyMessage(&msg)
	}
}

func (st *socketTransport) stop() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	st.setIsConnected(false)
	st.setIsListening(false)
	st.setIsWriting(false)
}

func (st *socketTransport) shutdown() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	st.setIsConnected(false)
	st.setIsListening(false)
	st.setIsWriting(false)
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
	go st.connect(false)
}

func (st *socketTransport) connect(initial bool) error {
	b := getBackoff()
	rand.Seed(time.Now().UTC().UnixNano())
	st.setIsConnected(false)
	st.setIsConnecting(true)
	defer st.setIsConnecting(false)

	for {
		nextItvl := b.Duration()
		err := st.dial()

		if err != nil {
			if initial {
				return err
			}
			fmt.Println(err)
			fmt.Println("Error connecting - will try again in ", nextItvl, "seconds.")
			time.Sleep(nextItvl)
			continue
		}

		fmt.Printf("Dial: connection was successfully established with %s\n", st.url.String())
		st.setIsConnected(true)
		go st.listen()
		go st.writer()
		return nil
	}
}

func (st *socketTransport) getIsConnected() bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.isConnected
}

func (st *socketTransport) getIsConnecting() bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.isConnecting
}

func (st *socketTransport) getIsListening() bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.isListening
}

func (st *socketTransport) getIsWriting() bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.isWriting
}

func (st *socketTransport) setIsConnected(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isConnected = state
	if state {
		st.cr.NotifyConnect()
	}
}

func (st *socketTransport) setIsConnecting(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isConnecting = state
}

func (st *socketTransport) setIsListening(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isListening = state
}

func (st *socketTransport) setIsWriting(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isWriting = state
}

func getBackoff() backoff.Backoff {
	return backoff.Backoff{
		Min:    5 * time.Second,
		Max:    30 * time.Second,
		Factor: 2,
		Jitter: true,
	}
}

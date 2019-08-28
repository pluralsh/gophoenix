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

// Logger is an interface for a pluggable logger component to be passed to the client
type Logger interface {
	Debug(...interface{})
	Debugf(string, ...interface{})
	Error(...interface{})
	Errorf(string, ...interface{})
	Info(...interface{})
	Infof(string, ...interface{})
	Warn(...interface{})
	Warnf(string, ...interface{})
}

type socketTransport struct {
	socket       *websocket.Conn
	dialer       *websocket.Dialer
	cr           ConnectionReceiver
	mr           MessageReceiver
	close        chan struct{}
	done         chan struct{}
	reconnect    chan struct{}
	logger       Logger
	isConnected  bool
	isConnecting bool
	mu           sync.RWMutex
	url          url.URL
	header       http.Header
	backoff      *backoff.Backoff
}

func (st *socketTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	st.mr = mr
	st.cr = cr
	st.url = url
	st.header = header

	return st.connect(true)
}

func (st *socketTransport) Push(data *Message) error {
	if err := st.socket.WriteJSON(data); err != nil {
		st.logger.Warn("WriteJSON error: ", err.Error())
		return err
	}
	return nil
}

func (st *socketTransport) Close() {
	st.shutdown()
}

func (st *socketTransport) writer() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		fmt.Println("Stopping writer...")
		ticker.Stop()
	}()

	for {
		select {
		case <-st.close:
			return
		case <-st.done:
			return
		case <-ticker.C:
			if err := st.Push(&Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				st.logger.Warn("Error sending heartbeat:", err.Error())
				st.reconnect <- struct{}{}
			}
		}
	}
}

func (st *socketTransport) listen() {
	st.socket.SetReadLimit(maxMessageSize)

	for {
		var msg Message
		if err := st.socket.ReadJSON(&msg); err != nil {
			st.logger.Warn("Error reading from socket.  Retrying connection: ", err)
			st.reconnect <- struct{}{}
		}

		st.mr.NotifyMessage(&msg)
	}
}

func (st *socketTransport) shutdown() {
	st.socket.Close()
	st.cr.NotifyDisconnect()
	st.done <- struct{}{}
	st.setIsConnected(false)
}

func (st *socketTransport) dial() error {
	if st.getIsConnecting() {
		return nil
	}

	st.setIsConnected(false)
	st.setIsConnecting(true)

	conn, _, err := st.dialer.Dial(st.url.String(), st.header)

	if err != nil {
		return err
	}

	st.setSocket(conn)
	st.setIsConnected(true)

	return err
}

func (st *socketTransport) connect(initial bool) error {
	b := st.getBackoff()
	rand.Seed(time.Now().UTC().UnixNano())

	for {
		nextItvl := b.Duration()
		err := st.dial()

		if err != nil {
			if initial {
				return err
			}
			st.logger.Warnf("Error connecting - will try again in %v, seconds: %s\n", nextItvl, err)
			time.Sleep(nextItvl)
			continue
		}

		st.logger.Debugf("Dial: connection was successfully established with %s\n", st.url.String())
		break
	}

	b.Reset()
	go st.listen()
	go st.writer()
	go st.supervisor()
	return nil
}

func (st *socketTransport) supervisor() {
	for {
		select {
		case <-st.close:
			return
		case <-st.done:
			return
		case <-st.reconnect:
			if st.getIsConnecting() {
				continue
			}

			err := st.dial()
			if err != nil {
				duration := st.getBackoff().Duration()
				st.logger.Warnf("Unable to establish connection, retrying in %s seconds", duration)
				time.Sleep(duration)
				st.reconnect <- struct{}{}
			} else {
				st.getBackoff().Reset()
			}
		}
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

func (st *socketTransport) setSocket(conn *websocket.Conn) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.socket = conn
}

func (st *socketTransport) getBackoff() *backoff.Backoff {
	if st.backoff != nil {
		return &backoff.Backoff{
			Min:    5 * time.Second,
			Max:    30 * time.Second,
			Factor: 2,
			Jitter: true,
		}
	}

	return st.backoff
}

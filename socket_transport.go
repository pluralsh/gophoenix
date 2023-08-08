package gophoenix

import (
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
)

const (
	// Time allowed to read the next pong message from the peer.
	pongWait = 120 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = 40 * time.Second

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
	socket         *websocket.Conn
	dialer         *websocket.Dialer
	cr             ConnectionReceiver
	mr             MessageReceiver
	close          chan struct{}
	done           chan struct{}
	reconnect      chan struct{}
	logger         Logger
	isConnected    bool
	isConnecting   bool
	isReconnecting bool
	mu             sync.RWMutex
	url            url.URL
	header         http.Header
	backoff        *backoff.Backoff
	lastHeartbeat  *atomic.Int64
}

func (st *socketTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	st.mr = mr
	st.cr = cr
	st.url = url
	st.header = header

	return st.connect()
}

func (st *socketTransport) Push(data *Message) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if err := st.socket.WriteJSON(data); err != nil {
		st.logger.Warn("Error sending message via socket: ", err.Error())
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
		ticker.Stop()
	}()

	for {
		select {
		case <-st.close:
			return
		case <-st.done:
			return
		case <-ticker.C:
			if st.getIsConnecting() {
				continue
			}

			lastBeat := time.UnixMilli(st.lastHeartbeat.Load())

			if time.Now().Sub(lastBeat) > pongWait && !st.getIsReconnecting() {
				st.logger.Infof("Last heartbeat reply was at %v (%d seconds ago), exceeding deadline of %d seconds. Reconnecting socket", lastBeat, time.Now().Sub(lastBeat).Seconds(), pongWait.Seconds())
				st.reconnect <- struct{}{}
				continue
			}

			if err := st.Push(&Message{Topic: "phoenix", Event: "heartbeat", Payload: nil, Ref: -1}); err != nil {
				st.logger.Warn("Error sending heartbeat: ", err)
			}
		}
	}
}

func (st *socketTransport) listen() {
	st.socket.SetReadLimit(maxMessageSize)

	for {
		var msg Message

		// While we don't have a connection, do not attempt to read
		if st.getIsConnecting() || st.getIsReconnecting() {
			time.Sleep(time.Second * 3)
			continue
		}

		if err := st.socket.ReadJSON(&msg); err != nil {
			st.logger.Warn("Error reading from socket.  Retrying connection: ", err)
			if !st.getIsReconnecting() {
				st.reconnect <- struct{}{}
			}
			time.Sleep(time.Second * 1)
		}
		if isHeartbeatResponse(&msg) {
			st.handleHeartbeatResponse(&msg)
		}

		st.mr.NotifyMessage(&msg)
	}
}

func (st *socketTransport) handleHeartbeatResponse(msg *Message) {
	if body, ok := msg.Payload.(map[string]interface{}); ok {
		if status, ok := body["status"].(string); ok && status == "ok" {
			st.lastHeartbeat.Store(time.Now().UnixMilli())
		}
	}
}

func isHeartbeatResponse(msg *Message) bool {
	return msg.Ref == -1 && msg.Topic == "phoenix" && msg.Event == "phx_reply"
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
	defer st.setIsConnecting(false)

	conn, _, err := st.dialer.Dial(st.url.String(), st.header)

	if err != nil {
		return err
	}

	st.setSocket(conn)
	st.setIsConnected(true)

	return err
}

func (st *socketTransport) connect() error {
	rand.Seed(time.Now().UTC().UnixNano())

	for {
		err := st.dial()

		if err != nil {
			return err
		}

		st.logger.Debug("Connection was successfully established with socket")
		break
	}

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
			st.socket.Close()
			st.setIsConnected(false)
			st.setIsReconnecting(true)
			st.cr.NotifyDisconnect()

			for {
				err := st.dial()
				if err != nil {
					duration := st.getBackoff().Duration()
					st.logger.Debugf("Unable to reconnect to socket.  Attempting again in %s\n", duration)
					time.Sleep(duration)
					continue
				}

				st.lastHeartbeat.Store(time.Now().UnixMilli())
				st.setIsReconnecting(false)
				st.getBackoff().Reset()
				break
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

func (st *socketTransport) getIsReconnecting() bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.isReconnecting
}

func (st *socketTransport) setIsConnected(state bool) {
	st.mu.Lock()
	st.isConnected = state
	st.mu.Unlock()

	if state {
		time.Sleep(time.Millisecond * 100)
		st.cr.NotifyConnect()
	}
}

func (st *socketTransport) setIsConnecting(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isConnecting = state
}

func (st *socketTransport) setIsReconnecting(state bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.isReconnecting = state
}

func (st *socketTransport) setSocket(conn *websocket.Conn) {
	st.mu.Lock()
	defer st.mu.Unlock()

	st.socket = conn
}

func (st *socketTransport) getBackoff() *backoff.Backoff {
	if st.backoff == nil {
		b := &backoff.Backoff{
			Min:    5 * time.Second,
			Max:    30 * time.Second,
			Factor: 2,
			Jitter: true,
		}
		st.backoff = b
	}

	return st.backoff
}

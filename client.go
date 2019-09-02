package gophoenix

import (
	"errors"
	"math/rand"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
)

// Client is the entry point for a phoenix channel connection.
type Client struct {
	t  Transport
	mr *messageRouter
	cr ConnectionReceiver
}

// NewWebsocketClient creates the default connection using a websocket as the transport.
func NewWebsocketClient(d *websocket.Dialer, cr ConnectionReceiver, logger Logger) *Client {
	return &Client{
		t: &socketTransport{
			dialer:    d,
			done:      make(chan struct{}),
			close:     make(chan struct{}),
			reconnect: make(chan struct{}),
			logger:    logger,
		},
		cr: cr,
		mr: newMessageRouter(),
	}
}

// Connect should be called to establish the connection through the transport.
func (c *Client) Connect(url url.URL, header http.Header) error {
	if c.t == nil {
		return errors.New("transport not provided")
	}

	return c.t.Connect(url, header, c.mr, c.cr)
}

// Close closes the connection via the transport.
func (c *Client) Close() error {
	if c.t == nil {
		return errors.New("transport not provided")
	}

	c.t.Close()

	return nil
}

// Join subscribes to a channel via the transport and returns a reference to the channel.
func (c *Client) Join(callbacks ChannelReceiver, topic string, payload interface{}) (*Channel, error) {
	if c.t == nil {
		return nil, errors.New("transport not provided")
	}

	rc := &atomicRef{ref: new(int64)}
	joinRef := rand.Int63()

	rr := newReplyRouter()
	ch := &Channel{topic: topic, t: c.t, rc: rc, joinRef: joinRef, rr: rr, ln: func() { c.mr.unsubscribe(topic) }}
	c.mr.subscribe(topic, callbacks, rr)
	err := ch.join(payload)

	if err != nil {
		return nil, err
	}

	return ch, nil
}

package gophoenix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jpillora/backoff"
)

type longPollTransport struct {
	cr             ConnectionReceiver
	mr             MessageReceiver
	close          chan struct{}
	done           chan struct{}
	reconnect      chan struct{}
	logger         Logger
	isConnected    bool
	isConnecting   bool
	isReconnecting bool
	awaitingAck    bool
	mu             sync.RWMutex
	url            url.URL
	header         http.Header
	backoff        *backoff.Backoff
	client         *http.Client
}

type messagesResp struct {
	Status   int       `json:"status"`
	Token    int       `json:"token"`
	Messages []Message `json:"messages"`
}

func (lt *longPollTransport) Connect(url url.URL, header http.Header, mr MessageReceiver, cr ConnectionReceiver) error {
	lt.mr = mr
	lt.cr = cr
	lt.url = url
	lt.header = header

	go lt.supervisor()

	// Need to validate URL, connect to endpoint, and start polling
	return lt.connect()
}

func (lt *longPollTransport) connect() error {
	rand.Seed(time.Now().UTC().UnixNano())

	go func() {
		for {
			pollError := lt.poll()
			if pollError != nil {
				lt.logger.Debug("Connection was successfully established with channel")
				break
			}
		}

		// Telling long-poll client to reconnect
		lt.reconnect <- struct{}{}
	}()

	return nil
}

func (lt *longPollTransport) poll() error {
	var msgResp messagesResp
	req, err := http.NewRequest(http.MethodGet, lt.url.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Add("content-type", "application/json")

	resp, err := lt.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	err = json.Unmarshal(bodyBytes, &msgResp)
	if err != nil {
		return err
	}

	for _, msg := range msgResp.Messages {
		lt.mr.NotifyMessage(&msg)
	}

	return err
}

func (lt *longPollTransport) getMessages() {

}

func (lt *longPollTransport) Push(data *Message) error {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	msgBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, lt.url.String(), bytes.NewReader(msgBytes))
	if err != nil {
		return err
	}

	req.Header.Add("content-type", "application/x-ndjson")

	resp, err := lt.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode > 204 {
		return fmt.Errorf("received %d response from server after retries, message not delivered", resp.StatusCode)
	}

	return nil
}

func (lt *longPollTransport) Close() {
	lt.shutdown()
}

func (lt *longPollTransport) shutdown() {
	lt.cr.NotifyDisconnect()
	lt.done <- struct{}{}
	lt.setIsConnected(false)
}

func (lt *longPollTransport) supervisor() {
	for {
		select {
		case <-lt.close:
			return
		case <-lt.done:
			return
		case <-lt.reconnect:
			lt.setIsConnected(false)
			lt.setIsReconnecting(true)
			lt.cr.NotifyDisconnect()

			for {
				err := lt.connect()
				if err != nil {
					duration := lt.getBackoff().Duration()
					lt.logger.Debugf("Unable to restart long-polling to get messages.  Attempting again in %s\n", duration)
					time.Sleep(duration)
					continue
				}

				lt.setIsReconnecting(false)
				lt.getBackoff().Reset()
				break
			}
		}
	}
}

func (lt *longPollTransport) getIsConnected() bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	return lt.isConnected
}

func (lt *longPollTransport) getIsConnecting() bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	return lt.isConnecting
}

func (lt *longPollTransport) getIsReconnecting() bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	return lt.isReconnecting
}

func (lt *longPollTransport) setIsConnected(state bool) {
	lt.mu.Lock()
	lt.isConnected = state
	lt.mu.Unlock()

	if state {
		time.Sleep(time.Millisecond * 100)
		lt.cr.NotifyConnect()
	}
}

func (lt *longPollTransport) setIsConnecting(state bool) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	lt.isConnecting = state
}

func (lt *longPollTransport) setIsReconnecting(state bool) {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	lt.isReconnecting = state
}

func (lt *longPollTransport) getBackoff() *backoff.Backoff {
	if lt.backoff == nil {
		b := &backoff.Backoff{
			Min:    5 * time.Second,
			Max:    30 * time.Second,
			Factor: 2,
			Jitter: true,
		}
		lt.backoff = b
	}

	return lt.backoff
}

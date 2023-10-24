package gophoenix

import (
	"fmt"
	"strconv"
)

var protocolError = fmt.Errorf("protocol error")

// Message is a message sent or received via the Transport from the channel.
type Message struct {
	Topic   string      `json:"topic"`
	Event   string      `json:"event"`
	Payload interface{} `json:"payload"`
	Ref     int64       `json:"ref"`
	JoinRef int64       `json:"join_ref,omitempty"`
}

func FromRaw(arr []interface{}) (msg Message, err error) {
	if len(arr) != 5 {
		err = protocolError
		return
	}

	if arr[2] == nil || arr[3] == nil {
		err = protocolError
		return
	}

	msg.Ref = parseRef(arr[1])
	msg.JoinRef = parseRef(arr[0])
	msg.Topic = arr[2].(string)
	msg.Event = arr[3].(string)
	msg.Payload = arr[4].(map[string]interface{})
	return
}

func parseRef(ref interface{}) (res int64) {
	if ref == nil {
		return
	}
	maybe, err := strconv.Atoi(ref.(string))
	if err != nil {
		return
	}
	res = int64(maybe)
	return
}

func ToRaw(msg *Message) [5]interface{} {
	var m [5]interface{}

	m[0] = strconv.Itoa(int(msg.JoinRef))
	m[1] = strconv.Itoa(int(msg.Ref))
	m[2] = msg.Topic
	m[3] = msg.Event
	m[4] = msg.Payload
	return m
}

package gophoenix

// Channel represents a subscription to a topic. It is returned from the Client after joining a topic.
type Channel struct {
	topic string
	t     Transport
	rc    refCounter
	rr    *replyRouter
	ln    leaveNotifier
}

type leaveNotifier func()

type refCounter interface {
	nextRef() int64
}

// Unsubscribe clears local state related to the channel, to be called when there
// is an unexpected disconnect from the socket
func (ch *Channel) Unsubscribe() {
	ch.ln()
}

// Leave notifies the channel to unsubscribe from messages on the topic.
func (ch *Channel) Leave(payload interface{}) error {
	defer ch.ln()
	ref := ch.rc.nextRef()
	return ch.sendMessage(ref, string(LeaveEvent), payload)
}

// Push sends a message on the topic.
func (ch *Channel) Push(event string, payload interface{}, replyHandler func(payload interface{})) error {
	ref := ch.rc.nextRef()
	ch.rr.subscribe(ref, replyHandler)
	return ch.sendMessage(ref, event, payload)
}

// Reply sends a message on the topic.
func (ch *Channel) Reply(ref int64, channel string, event string, payload interface{}, replyHandler func(payload interface{})) error {
	msg := &Message{
		Topic:   channel,
		Event:   event,
		Payload: payload,
		Ref:     ref,
	}

	return ch.t.Push(msg)
}

// PushNoReply sends a message on the topic but does not provide a callback to receive replies.
func (ch *Channel) PushNoReply(event string, payload interface{}) error {
	ref := ch.rc.nextRef()
	return ch.sendMessage(ref, event, payload)
}

func (ch *Channel) join(payload interface{}) error {
	return ch.sendJoinMessage(payload)
}

func (ch *Channel) sendJoinMessage(payload interface{}) error {
	ref := ch.rc.nextRef()
	joinRef := ch.rc.nextRef()

	msg := &Message{
		Topic:   ch.topic,
		Event:   string(JoinEvent),
		Payload: payload,
		Ref:     ref,
		JoinRef: joinRef,
	}

	return ch.t.Push(msg)
}

func (ch *Channel) sendMessage(ref int64, event string, payload interface{}) error {
	msg := &Message{
		Topic:   ch.topic,
		Event:   event,
		Payload: payload,
		Ref:     ref,
	}

	return ch.t.Push(msg)
}

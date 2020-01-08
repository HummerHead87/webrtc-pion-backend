package chat_server

import (
	"context"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/vektah/gqlparser/gqlerror"
)

func (r *mutationResolver) PostMessage(ctx context.Context, roomName string, user string, text string) (*Message, error) {
	rms.Lock()
	rm, ok := rms.items[roomName]
	rms.Unlock()
	if !ok {
		return nil, gqlerror.Errorf("room with name '%v' doesn't exists", roomName)
	}

	// Create message
	m := &Message{
		ID:        ksuid.New().String(),
		CreatedAt: time.Now().UTC(),
		Text:      text,
		User:      user,
	}

	rm.Lock()
	for _, ch := range rm.chatMessageChannels {
		ch <- m
	}
	rm.Unlock()

	return m, nil
}

func (r *subscriptionResolver) MessagePosted(ctx context.Context, roomName *string) (<-chan *Message, error) {
	rms.Lock()
	rm, ok := rms.items[*roomName]
	rms.Unlock()
	if !ok {
		return nil, gqlerror.Errorf("room with name '%v' doesn't exists", roomName)
	}

	ch := make(chan *Message, 1)
	id := ksuid.New().String()

	rm.Lock()
	rm.chatMessageChannels[id] = ch
	rm.Unlock()

	go func() {
		<-ctx.Done()
		rm.Lock()
		delete(rm.chatMessageChannels, id)
		rm.Unlock()
	}()

	return ch, nil
}

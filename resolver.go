//go:generate gorunpkg github.com/vektah/gqlgen
package chat_server

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/common/log"
	"github.com/segmentio/ksuid"
	"github.com/shirou/gopsutil/cpu"
) // THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

type Resolver struct {
	serverCPUChannels map[string]chan []float64
	mutex             sync.Mutex
}

func NewResolver() (*Resolver, error) {
	resolver := &Resolver{
		serverCPUChannels: map[string]chan []float64{},
	}
	err := resolver.init()
	if err != nil {
		return nil, err
	}

	return resolver, nil
}

func (r *Resolver) init() error {
	ticker := time.NewTicker(1 * time.Second)

	go func() {
		for range ticker.C {
			percent, err := cpu.Percent(1*time.Second, true)
			if err != nil {
				log.Errorf("unable to get CPU stats: %s", err)
				continue
			}

			r.mutex.Lock()
			for _, ch := range r.serverCPUChannels {
				ch <- percent
			}
			r.mutex.Unlock()
		}
	}()

	return nil
}

func (r *Resolver) Mutation() MutationResolver {
	return &mutationResolver{r}
}
func (r *Resolver) Query() QueryResolver {
	return &queryResolver{r}
}
func (r *Resolver) Subscription() SubscriptionResolver {
	return &subscriptionResolver{r}
}

type mutationResolver struct{ *Resolver }

func (r *mutationResolver) PostMessage(ctx context.Context, user string, text string) (*Message, error) {
	panic("not implemented")
}

type queryResolver struct{ *Resolver }

func (r *queryResolver) Messages(ctx context.Context) ([]*Message, error) {
	panic("not implemented")
}
func (r *queryResolver) Users(ctx context.Context) ([]string, error) {
	panic("not implemented")
}

type subscriptionResolver struct{ *Resolver }

func (r *subscriptionResolver) MessagePosted(ctx context.Context, user string) (<-chan *Message, error) {
	panic("not implemented")
}
func (r *subscriptionResolver) UserJoined(ctx context.Context, user string) (<-chan string, error) {
	panic("not implemented")
}
func (r *subscriptionResolver) ServerCPU(ctx context.Context) (<-chan []float64, error) {
	ch := make(chan []float64, 1)
	userID := ksuid.New().String()

	r.mutex.Lock()
	r.serverCPUChannels[userID] = ch
	r.mutex.Unlock()

	// Delete channel when done
	go func() {
		<-ctx.Done()
		r.mutex.Lock()
		delete(r.serverCPUChannels, userID)
		r.mutex.Unlock()
	}()

	return ch, nil
}

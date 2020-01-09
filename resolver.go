//go:generate gorunpkg github.com/vektah/gqlgen
package chat_server

import (
	"context"
	"sync"
	"time"

	"github.com/pion/webrtc/v2"
	"github.com/prometheus/common/log"
	"github.com/segmentio/ksuid"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
) // THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

type Resolver struct {
	serverUsageChannels map[string]chan *ServerUsage
	mutex               sync.Mutex
	mEngine             webrtc.MediaEngine
	api                 *webrtc.API
}

func NewResolver(mEngine webrtc.MediaEngine, api *webrtc.API) (*Resolver, error) {
	resolver := &Resolver{
		serverUsageChannels: map[string]chan *ServerUsage{},
		mEngine:             mEngine,
		api:                 api,
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

			ram, err := mem.VirtualMemory()
			if err != nil {
				log.Errorf("unable to get RAM usage stats: %s", err)
				continue
			}

			su := &ServerUsage{
				CPU: percent,
				RAM: &RAMUsage{
					Total:       int(bToMb(ram.Total)),
					Used:        int(bToMb(ram.Used)),
					UsedPercent: ram.UsedPercent,
				},
			}

			r.mutex.Lock()
			for _, ch := range r.serverUsageChannels {
				ch <- su
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

type queryResolver struct{ *Resolver }

type subscriptionResolver struct{ *Resolver }

func (r *subscriptionResolver) ServerLoad(ctx context.Context) (<-chan *ServerUsage, error) {
	ch := make(chan *ServerUsage, 10)
	userID := ksuid.New().String()

	r.mutex.Lock()
	r.serverUsageChannels[userID] = ch
	r.mutex.Unlock()

	// Delete channel when done
	go func() {
		<-ctx.Done()
		r.mutex.Lock()
		delete(r.serverUsageChannels, userID)
		r.mutex.Unlock()
	}()

	return ch, nil
}

func bToMb(val uint64) uint64 {
	return val / 1024 / 1024
}

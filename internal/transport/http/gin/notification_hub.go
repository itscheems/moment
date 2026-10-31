package ginhttp

import (
	"context"
	"sync"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

type notificationUpdate struct {
	state app.NotificationState
	err   error
}
type notificationSubscription struct {
	actor   domain.User
	updates chan notificationUpdate
}

// The observer reads durable state so direct commands in other processes are visible.
// A single timer serves all subscribers and each account is sampled once per tick.
type notificationHub struct {
	mu            sync.Mutex
	service       *app.InteractionService
	subscriptions map[*notificationSubscription]struct{}
	running       bool
}

func newNotificationHub(service *app.InteractionService) *notificationHub {
	return &notificationHub{service: service, subscriptions: make(map[*notificationSubscription]struct{})}
}
func (hub *notificationHub) subscribe(ctx context.Context) (<-chan notificationUpdate, func()) {
	actor, _ := app.ContentActor(ctx)
	sub := &notificationSubscription{actor: actor, updates: make(chan notificationUpdate, 1)}
	hub.mu.Lock()
	hub.subscriptions[sub] = struct{}{}
	if !hub.running {
		hub.running = true
		go hub.run()
	}
	hub.mu.Unlock()
	return sub.updates, func() { hub.mu.Lock(); delete(hub.subscriptions, sub); hub.mu.Unlock() }
}
func (hub *notificationHub) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		hub.mu.Lock()
		if len(hub.subscriptions) == 0 {
			hub.running = false
			hub.mu.Unlock()
			return
		}
		subs := make([]*notificationSubscription, 0, len(hub.subscriptions))
		for sub := range hub.subscriptions {
			subs = append(subs, sub)
		}
		hub.mu.Unlock()
		states := make(map[string]notificationUpdate)
		for _, sub := range subs {
			update, ok := states[sub.actor.ID]
			if !ok {
				ctx, cancel := context.WithTimeout(app.WithContentActor(context.Background(), sub.actor), 2*time.Second)
				update.state, update.err = hub.service.NotificationState(ctx)
				cancel()
				states[sub.actor.ID] = update
			}
			select {
			case sub.updates <- update:
			default:
				select {
				case <-sub.updates:
				default:
				}
				select {
				case sub.updates <- update:
				default:
				}
			}
		}
	}
}

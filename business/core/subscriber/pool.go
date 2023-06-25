// Package subscriber represents subscriber entities and pool of subscribers.
package subscriber

import (
	"context"
	"errors"
	"fmt"
	"github.com/manmolecular/go-now-here/business/core/message"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"sync"
	"time"
)

// Group of subscriber pool configuration parameters.
const (
	// publishLimiterMilliseconds defines rate limit for a message publishing
	publishLimiterMilliseconds = 100

	// publishLimiterBurst allows burst of messages of size N
	publishLimiterBurst = 10

	// checkSubAvailabilityMilliseconds defines rate of available subscribers checking interval
	checkSubAvailabilityMilliseconds = 500

	// CheckSubAvailabilityMaxTimeSeconds defines how long can we wait for an available subscriber
	CheckSubAvailabilityMaxTimeSeconds = 10
)

// searchPairTimeoutError represents search pair error
const searchPairTimeoutError = "pair search time out reached: pair can not be found"

type SubscribersPool struct {
	log           *zap.SugaredLogger
	subscribersMu sync.Mutex

	// publishLimiter controls the rate limit applied to the publishMessage function.
	publishLimiter *rate.Limiter

	// SubscriberMessageBuffer controls the max number of messages that can be queued for a subscriber.
	SubscriberMessageBuffer int
	Subscribers             map[string]*Subscriber
}

// NewSubscribersPool constructs a pool of subscribers.
func NewSubscribersPool(log *zap.SugaredLogger) *SubscribersPool {
	limiter := rate.NewLimiter(rate.Every(publishLimiterMilliseconds*time.Millisecond), publishLimiterBurst)

	return &SubscribersPool{
		log:            log,
		Subscribers:    make(map[string]*Subscriber),
		publishLimiter: limiter,
	}
}

// PublishMessage publishes a message from a subscriber to the corresponding subscriber pair.
func (s *SubscribersPool) PublishMessage(msg *message.Message, sub *Subscriber) error {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()

	if err := s.publishLimiter.Wait(context.Background()); err != nil {
		return fmt.Errorf("message %s can not be published, limiter error: %w", msg, err)
	}

	// Message can be published to the paired subscriber only and only if pair exists
	sPair, ok := s.Subscribers[sub.PairId]
	if !ok {
		return fmt.Errorf("message %s can not be published, no available pair to send message to", msg)
	}

	sPair.Messages <- msg
	s.log.Debugw("message sent successfully", "fromId", sub.Id, "toId", sPair.Id)

	return nil
}

// LinkSubscribers links two available SubscribersPool with each other as a pair.
func (s *SubscribersPool) LinkSubscribers(firstId, secondId string) {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()

	s.Subscribers[secondId].PairId = firstId
	s.Subscribers[firstId].PairId = secondId

	s.log.Debugw("successfully linked two SubscribersPool",
		"firstId", s.Subscribers[firstId].Id,
		"secondId", s.Subscribers[secondId].Id)
}

// AddSubscriber adds a subscriber to the pool.
func (s *SubscribersPool) AddSubscriber(sub *Subscriber) {
	s.subscribersMu.Lock()
	s.Subscribers[sub.Id] = sub
	s.subscribersMu.Unlock()
}

// DeleteSubscriber deletes the given subscriber from the pool.
func (s *SubscribersPool) DeleteSubscriber(sub *Subscriber) {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()
	sPair, ok := s.Subscribers[sub.PairId]
	if ok {
		sPair.PairDisconnected <- true
	}
	delete(s.Subscribers, sub.Id)
}

// AssignSubscriber assigns an available subscriber to a newcomer subscriber.
func (s *SubscribersPool) AssignSubscriber(sub *Subscriber) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticker := time.NewTicker(checkSubAvailabilityMilliseconds * time.Millisecond)
	defer ticker.Stop()

	isFound := make(chan bool, 1)

	go func() {
		for range ticker.C {
			// Stop search If the current subscriber already has an assigned pair, mark as found
			if sub.PairId != "" {
				s.log.Debugw("subscriber has a pair", "subscriberId", sub.Id, "pairId", sub.PairId)
				isFound <- true
				return
			}
			select {
			case <-ctx.Done():
				s.log.Debugw("pair search is finished (found or timed out)", "subscriberId", sub.Id, "reason", "foundOrTimeout")
				return
			default:
				s.log.Debugw("search for a pair to assign to a subscriber", "subscriberId", sub.Id)
				for idAnother, sAnother := range s.Subscribers {
					// Skip another subscriber if he has a pair or if it is a current subscriber
					if sAnother.PairId != "" || idAnother == sub.Id {
						continue
					}

					// The current subscriber and another one are both able to be linked with each other, mark as found
					s.LinkSubscribers(sub.Id, sAnother.Id)
					isFound <- true
				}
			}
		}
	}()

	select {
	case <-isFound:
		s.log.Debugw("pair found", "subscriberId", sub.Id, "pairId", sub.PairId)
		return nil
	case <-time.After(CheckSubAvailabilityMaxTimeSeconds * time.Second):
		s.log.Debugw(searchPairTimeoutError, "subscriberId", sub.Id)
		return errors.New(searchPairTimeoutError)
	}
}

package breaker

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// StateClosed represents the closed state of the circuit breaker.
	StateClosed int32 = iota
	// StateOpen represents the open state of the circuit breaker.
	StateOpen
	// StateHalfOpen represents the half-open state of the circuit breaker.
	StateHalfOpen
)



// Requester is an interface for making HTTP requests.
type Requester interface {
	Do(req *http.Request) (*http.Response, error)
}

// Breaker is a circuit breaker implementation.
type Breaker struct {
	requester         Requester
	state             int32
	lastFailureTime   time.Time
	failureThreshold  int64
	consecutiveFailures int64
	openStateDuration time.Duration
	mu                sync.RWMutex
}

// NewBreaker creates a new circuit breaker.
func NewBreaker(requester Requester, failureThreshold int64, openStateDuration time.Duration) *Breaker {
	return &Breaker{
		requester:         requester,
		state:             StateClosed,
		failureThreshold:  failureThreshold,
		openStateDuration: openStateDuration,
	}
}

// DoRequest sends an HTTP request through the circuit breaker.
func (b *Breaker) DoRequest(req *http.Request) (*http.Response, error) {
	switch atomic.LoadInt32(&b.state) {
	case StateOpen:
		if time.Since(b.lastFailureTime) > b.openStateDuration {
			atomic.StoreInt32(&b.state, StateHalfOpen)
			return b.doHalfOpenRequest(req)
		}
		return nil, ErrOpenState
	case StateHalfOpen:
		return b.doHalfOpenRequest(req)
	case StateClosed:
		return b.doClosedRequest(req)
	default:
		return nil, errors.New("unknown state")
	}
}

func (b *Breaker) doClosedRequest(req *http.Request) (*http.Response, error) {
	resp, err := b.requester.Do(req)
	if err != nil {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.consecutiveFailures++
		if b.consecutiveFailures >= b.failureThreshold {
			atomic.StoreInt32(&b.state, StateOpen)
			b.lastFailureTime = time.Now()
		}
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	return resp, nil
}

func (b *Breaker) doHalfOpenRequest(req *http.Request) (*http.Response, error) {
	resp, err := b.requester.Do(req)
	if err != nil {
		atomic.StoreInt32(&b.state, StateOpen)
		b.lastFailureTime = time.Now()
		return nil, err
	}

	atomic.StoreInt32(&b.state, StateClosed)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	return resp, nil
}
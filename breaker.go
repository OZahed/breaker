/*
Package breaker provides a simple breaker implementation.

State transfer should be statistically evaluated to avoid false positives and negatives.
*/

package breaker

import (
	"math"
	"net/http"
	"sync"
	"time"
)

const (
	floatOne = 1.0
)

var (
	DefaultHalfOpenPercentages               = []float64{0.1, 0.3, 0.5, 0.75, 1.0}
	DefaultInterMediatoryStateChangeInterval = time.Second * 1
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

type Bucket struct {
	requests int
	failures int
}

type RetryPolicy struct {
	Count int
	Wait  time.Duration
}

type halfOpenInfo struct {
	LastHalfOpenRequest            time.Time
	HalfOpenStages                 []float64
	HalfOpenSubStateChangeInterval time.Duration
	CurrentPercentage              float64
	OnFlightRequest                float64
	MaxRequest                     float64
}

func (h *halfOpenInfo) NextStep() float64 {
	for idx, percent := range DefaultHalfOpenPercentages {
		if h.CurrentPercentage == percent {
			if idx == (len(h.HalfOpenStages) - 1) {
				return floatOne
			}

			return h.HalfOpenStages[idx+1]
		}
	}

	return 0
}

func (h *halfOpenInfo) ZeroState() {
	if h.HalfOpenSubStateChangeInterval <= 0 {
		h.HalfOpenSubStateChangeInterval = DefaultInterMediatoryStateChangeInterval
	}

	if len(h.HalfOpenStages) == 0 {
		h.HalfOpenStages = DefaultHalfOpenPercentages
	}

	h.LastHalfOpenRequest = time.Time{}
	h.OnFlightRequest = 0
	h.CurrentPercentage = h.HalfOpenStages[0]
}

// HttpRequester is the interface abstracting http.Client
// if you want to use monitoring and tracing, just implement them on http.Client and provide it to the breaker
type HttpRequester interface {
	Do(req *http.Request) (*http.Response, error)
}

type Breaker struct {
	lastStateChange      time.Time
	lastBucketTime       time.Time
	requester            HttpRequester
	halfOpenInfo         *halfOpenInfo
	buckets              []Bucket
	threshold            float64
	currentRate          float64
	stateStepInterval    time.Duration
	changeBucketDuration time.Duration
	windowInSeconds      int
	bucketPerSecond      int
	totalRequests        int
	totalFailures        int
	lastIndex            int
	currentState         State
	mu                   sync.RWMutex
}

func (cb *Breaker) ZeroState() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastBucketTime = time.Time{}
	cb.halfOpenInfo.ZeroState()
	for idx := range cb.buckets {
		cb.buckets[idx] = Bucket{}
	}

	cb.lastIndex = 0
	cb.currentRate = 1.0

	cb.totalFailures = 0
	cb.totalRequests = 0
}

// NewBreaker creates a new Breaker with the given windowInSeconds, bucketPerSecond and breakingThreshold.
// The windowInSeconds is the total time in seconds that the Breaker will keep track of.
// The bucketPerSecond is the number of buckets that the windowInSeconds will be divided into.
// The breakingThreshold is the percentage of failures that will cause the Breaker to open.
// The StateHandler is the handler that will be used to evaluate the state of the Breaker.
func NewBreaker(windowInSeconds, bucketsPerSecond int,
	threshold float64, stateStepInterval time.Duration, req HttpRequester) *Breaker {
	return &Breaker{
		windowInSeconds:   windowInSeconds,
		bucketPerSecond:   bucketsPerSecond,
		threshold:         threshold,
		stateStepInterval: stateStepInterval,
		buckets:           make([]Bucket, windowInSeconds*bucketsPerSecond),
		requester:         req,
	}
}

func (cb *Breaker) getBucketIndex() int {
	if cb.lastBucketTime.IsZero() {
		cb.lastBucketTime = time.Now()
		cb.buckets[cb.lastIndex] = Bucket{}
	}

	if time.Since(cb.lastBucketTime) < cb.changeBucketDuration {
		return cb.lastIndex
	}

	outDatedBucket := cb.buckets[cb.lastIndex]

	// clean up the outdated values
	cb.totalRequests -= outDatedBucket.requests
	cb.totalFailures -= outDatedBucket.failures

	// reset the bucket and recalculating the last index and current rate
	cb.lastIndex = (cb.lastIndex + 1) % len(cb.buckets)
	cb.buckets[cb.lastIndex] = Bucket{}

	cb.lastBucketTime = time.Now()

	cb.updateStats()
	return cb.lastIndex
}

// MakeRequest registers a request and a failure in the current bucket.
// It then updates the stats and evaluates the state of the Breaker.
// If the Breaker is in the Open state, it will return an error.
//
// Client is responsible for handling the error and determining which errors should be counted as
// error for breaker
// e.x:
//
//	err := cb.MakeRequest(&cb.RetryPolicy{Count: 3, Wait: time.Second*3},func() error {
//		res, err := http.Get("http://example.com")
//		if err != nil {
//			return err
//		}
//
//
//		// check the status code and return an error if it is not 200
//		if !(res.StatusCode >= 200 && res.StatusCode < 400){
//			return errors.New("server returned non-200 status code")
//		}
//
//		// read response body
//		defer res.Body.Close()
//		body, err := ioutil.ReadAll(res.Body)
//
//		// if you want to ignore bad response value for breaker, thats up to you
//		if err != nil {
//			return nil
//		}
//		return nil
//	})
func (cb *Breaker) Execute(f func() error) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	defer cb.StateEval()

	if !cb.Allow() {
		return ErrRequestDropped
	}

	idx := cb.getBucketIndex()

	cb.totalRequests++
	cb.buckets[idx].requests++

	err := f()
	if err != nil {
		cb.totalFailures++
		cb.buckets[idx].failures++
	}

	cb.updateStats()
	cb.StateEval()

	return err
}

func (cb *Breaker) DoRequest(req *http.Request) (resp *http.Response, err error) {
	err = cb.Execute(func() error {
		resp, err = cb.requester.Do(req)
		return err
	})
	return
}

func (cb *Breaker) updateStats() {
	cb.currentRate = float64(cb.totalFailures) / float64(cb.totalRequests)
}

func (cb *Breaker) Allow() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.currentState {
	case Closed:
		return cb.closedAllow()
	case Open:
		return false
	case HalfOpen:
		return cb.halfOpenAllow()
	default:
		return false
	}
}

func (cb *Breaker) closedAllow() bool {
	return cb.currentRate < cb.threshold
}

// TODO: check for todo section
// checko for HalfOpen allow
func (cb *Breaker) halfOpenAllow() bool {
	if time.Since(cb.halfOpenInfo.LastHalfOpenRequest) > cb.halfOpenInfo.HalfOpenSubStateChangeInterval {
		cb.checkHalfOpenState()
	}

	allowedReqNumbers := cb.halfOpenInfo.MaxRequest * cb.halfOpenInfo.CurrentPercentage

	if allowedReqNumbers < 1.0 {
		allowedReqNumbers = 1.0
	}

	return math.Abs(allowedReqNumbers-cb.halfOpenInfo.OnFlightRequest) < 0.01

}

func (cb *Breaker) checkHalfOpenState() {
	if (time.Since(cb.halfOpenInfo.LastHalfOpenRequest) >= cb.halfOpenInfo.HalfOpenSubStateChangeInterval) &&
		float64(cb.totalFailures)/float64(cb.totalRequests) > 0.9 {
		cb.halfOpenInfo.CurrentPercentage = cb.halfOpenInfo.NextStep()
	}

	if cb.halfOpenInfo.CurrentPercentage == 0 {
		return
	}

	if cb.halfOpenInfo.CurrentPercentage > 0.9 {
		cb.setState(Closed)
	}
}

func (cb *Breaker) setState(state State) {
	cb.ZeroState()
	cb.lastStateChange = time.Now()
	cb.currentState = state
}

// Bring everuything here
func (cb *Breaker) StateEval() {
	switch cb.currentState {
	case HalfOpen:
		cb.checkHalfOpenState()
	case Open:
		if time.Since(cb.lastStateChange) > cb.stateStepInterval {
			cb.setState(HalfOpen)
		}
	case Closed:
		if cb.currentRate < cb.threshold {
			cb.setState(Closed)
		}
	}
}

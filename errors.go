package breaker

import "errors"

var (
	ErrRateTooHigh      = errors.New("error rate too high")
	ErrRequestDropped   = errors.New("request dropped early by breaker")
	ErrThresholdTooHigh = errors.New("threshold too high")
)

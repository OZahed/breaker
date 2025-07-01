package breaker

import (
	"errors"
)

var (
	ErrOpenState     = errors.New("circuit breaker is in open state")
	ErrHalfOpenState = errors.New("circuit breaker is in half-open state")
)
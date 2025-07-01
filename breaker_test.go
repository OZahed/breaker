package breaker

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

type mockRequester struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockRequester) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func TestBreaker_ClosedState(t *testing.T) {
	requester := &mockRequester{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		},
	}

	breaker := NewBreaker(requester, 3, time.Second*5)
	_, err := breaker.DoRequest(&http.Request{})
	if err != nil {
		t.Errorf("Expected no error in closed state, but got %v", err)
	}
}

func TestBreaker_OpenState(t *testing.T) {
	requester := &mockRequester{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("request failed")
		},
	}

	breaker := NewBreaker(requester, 1, time.Second*5)
	_, err := breaker.DoRequest(&http.Request{})
	if err == nil {
		t.Errorf("Expected error in open state, but got nil")
	}

	_, err = breaker.DoRequest(&http.Request{})
	if err != ErrOpenState {
		t.Errorf("Expected ErrOpenState, but got %v", err)
	}
}

func TestBreaker_HalfOpenState(t *testing.T) {
	requester := &mockRequester{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("request failed")
		},
	}

	breaker := NewBreaker(requester, 1, time.Millisecond*100)
	_, err := breaker.DoRequest(&http.Request{})
	if err == nil {
		t.Errorf("Expected error, but got nil")
	}

	time.Sleep(time.Millisecond * 150)

	requester.doFunc = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	}

	_, err = breaker.DoRequest(&http.Request{})
	if err != nil {
		t.Errorf("Expected no error in half-open state after successful request, but got %v", err)
	}
}

func TestBreaker_OpenToClosedState(t *testing.T) {
	requester := &mockRequester{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("request failed")
		},
	}

	breaker := NewBreaker(requester, 1, time.Millisecond*100)
	_, err := breaker.DoRequest(&http.Request{})
	if err == nil {
		t.Errorf("Expected error, but got nil")
	}

	_, err = breaker.DoRequest(&http.Request{})
	if err != ErrOpenState {
		t.Errorf("Expected ErrOpenState, but got %v", err)
	}

	time.Sleep(time.Millisecond * 150)

	requester.doFunc = func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	}

	_, err = breaker.DoRequest(&http.Request{})
	if err != nil {
		t.Errorf("Expected no error in half-open state after successful request, but got %v", err)
	}
}

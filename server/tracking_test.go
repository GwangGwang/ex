package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/poll"

	"github.com/circleci/ex/testing/testcontext"
)

func TestTrackedListener(t *testing.T) {
	ctx, cancel := context.WithCancel(testcontext.Background())
	defer cancel()

	handled := make(chan struct{})
	handling := make(chan struct{})

	// make a server with a handler where we can control concurrent requests in flight
	s, err := NewServer(ctx, "test-server", "localhost:0",
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handling <- struct{}{}
			handled <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		}))
	assert.NilError(t, err)

	// and start the server
	go func() { _ = s.Serve() }() // (heh, looks a bit like Clojure)

	const (
		concurrency = 23 // we will send this may requests at once
		maxCons     = 15 // but use a client with only this many connections
	)

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxConnsPerHost = maxCons

	cl := http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}

	//cl := httpclient.New("test", "http://"+s.Addr().String(), "", "", time.Second)
	//err = cl.SetMaxConnectionsPerHost(maxCons)
	assert.NilError(t, err)

	// fire off all the requests - knowing that the
	for i := 0; i < concurrency; i++ {
		go func() {
			_, err := cl.Get(fmt.Sprintf("http://%s", s.Addr()))
			assert.NilError(t, err)
		}()
	}

	// wait for the calls from the now full pool to have have arrived
	for i := 0; i < maxCons; i++ {
		<-handling
	}

	// and confirm the server metrics
	gauges := s.MetricsProducer().Gauges(ctx)
	assert.Equal(t, gauges["total_connections"], float64(maxCons))
	assert.Equal(t, gauges["active_connections"], float64(maxCons))
	assert.Equal(t, gauges["number_of_remotes"], float64(1))
	assert.Equal(t, gauges["max_connections_per_remote"], float64(maxCons))
	assert.Equal(t, gauges["min_connections_per_remote"], float64(maxCons))

	// unblock the first batch - to allow the pool connections to be reused
	for i := 0; i < maxCons; i++ {
		<-handled
	}

	// make sure the second block are in flight
	for i := 0; i < concurrency-maxCons; i++ {
		<-handling
	}

	// Should still be within the pool limits, and no further connections opened
	gauges = s.MetricsProducer().Gauges(ctx)
	// the client uses the pooled connections so the total new connections does not grow
	assert.Equal(t, gauges["total_connections"], float64(maxCons))
	assert.Check(t, gauges["active_connections"] <= float64(maxCons))

	// make sure all remaining requests finish
	for i := 0; i < concurrency-maxCons; i++ {
		<-handled
	}

	gauges = s.MetricsProducer().Gauges(ctx)
	// the client won't have dropped all the pool connections yet
	assert.Check(t, gauges["active_connections"] <= float64(maxCons))

	// make sure the client closes the idle keep alive connections, so the server can see connections being closed
	cl.CloseIdleConnections()

	// See if our server notices all the active connections going away
	poll.WaitOn(t,
		func(t poll.LogT) poll.Result {
			gauges = s.MetricsProducer().Gauges(ctx)
			if gauges["active_connections"] == 0 {
				return poll.Success()
			}
			return poll.Continue("clients not closed yet")
		},
		poll.WithDelay(20*time.Millisecond), poll.WithTimeout(time.Second),
	)

	assert.Equal(t, gauges["number_of_remotes"], float64(0))
	assert.Equal(t, gauges["max_connections_per_remote"], float64(0))
	assert.Equal(t, gauges["min_connections_per_remote"], float64(0))
}

func TestTrackedListenerName(t *testing.T) {
	s, err := NewServer(context.Background(), "test-server", "localhost:0", nil)
	assert.NilError(t, err)
	assert.Equal(t, s.MetricsProducer().MetricName(), "test-server-listener")
}

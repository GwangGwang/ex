package honeycomb

import (
	"errors"
	"io"

	multierror "github.com/hashicorp/go-multierror"
	libhoney "github.com/honeycombio/libhoney-go"
	"github.com/honeycombio/libhoney-go/transmission"
)

// This has been submitted upstream as
// https://github.com/honeycombio/libhoney-go/pull/60

// newSender returns a transmission.Sender which sends traces to w, and when
// send is true, also sends traces to Honeycomb.
func newSender(w io.Writer, send bool) transmission.Sender {
	s := &MultiSender{}

	if send {
		s.Senders = append(s.Senders, &transmission.Honeycomb{
			MaxBatchSize:         libhoney.DefaultMaxBatchSize,
			BatchTimeout:         libhoney.DefaultBatchTimeout,
			MaxConcurrentBatches: libhoney.DefaultMaxConcurrentBatches,
			PendingWorkCapacity:  libhoney.DefaultPendingWorkCapacity,
			UserAgentAddition:    libhoney.UserAgentAddition,
		})
	}

	s.Senders = append(s.Senders, &transmission.WriterSender{W: w})
	return s
}

type MultiSender struct {
	Senders []transmission.Sender
}

// Add calls Add on every configured Sender
func (s *MultiSender) Add(ev *transmission.Event) {
	for _, tx := range s.Senders {
		tx.Add(ev)
	}
}

// Start calls Start on every configured Sender, aborting on the first error
func (s *MultiSender) Start() error {
	if len(s.Senders) == 0 {
		return errors.New("no senders configured")
	}
	for _, tx := range s.Senders {
		if err := tx.Start(); err != nil {
			return err
		}
	}
	return nil
}

// Stop calls Stop on every configured Sender.
// It will call Stop on every Sender even if there are errors
func (s *MultiSender) Stop() error {
	var result error
	for _, tx := range s.Senders {
		if err := tx.Stop(); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

// TxResponses returns the response channel from the first Sender only
func (s *MultiSender) TxResponses() chan transmission.Response {
	return s.Senders[0].TxResponses()
}

// SendResponse calls SendResponse on every configured Sender
func (s *MultiSender) SendResponse(resp transmission.Response) bool {
	pending := false
	for _, tx := range s.Senders {
		pending = pending || tx.SendResponse(resp)
	}
	return pending
}

func (s *MultiSender) Flush() error {
	var result error
	for _, tx := range s.Senders {
		if err := tx.Flush(); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

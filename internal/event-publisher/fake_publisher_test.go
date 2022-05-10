package publisher

import (
	"context"

	"github.com/containerd/containerd/events"
)

type fakePublisher struct {
	events []interface{}
}

var _ events.Publisher = &fakePublisher{}

func NewFakePublisher() *fakePublisher {
	return &fakePublisher{}
}

func (p *fakePublisher) Publish(ctx context.Context, topic string, event events.Event) (err error) {
	if p == nil {
		return nil
	}
	p.events = append(p.events, event)
	return nil
}

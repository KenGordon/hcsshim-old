package publisher

import (
	"context"
	"fmt"

	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	shim "github.com/containerd/containerd/runtime/v2/shim"
	"go.opencensus.io/trace"
)

type eventPublisher struct {
	namespace       string
	remotePublisher *shim.RemoteEventsPublisher
}

var _ events.Publisher = &eventPublisher{}

func NewEventPublisher(address, namespace string) (*eventPublisher, error) {
	p, err := shim.NewPublisher(address)
	if err != nil {
		return nil, err
	}
	return &eventPublisher{
		namespace:       namespace,
		remotePublisher: p,
	}, nil
}

func (e *eventPublisher) Close() error {
	return e.remotePublisher.Close()
}

func (e *eventPublisher) Publish(ctx context.Context, topic string, event events.Event) (err error) {
	ctx, span := trace.StartSpan(ctx, "Publish")
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()
	span.AddAttributes(
		trace.StringAttribute("topic", topic),
		trace.StringAttribute("event", fmt.Sprintf("%+v", event)))

	if e == nil {
		return nil
	}

	return e.remotePublisher.Publish(namespaces.WithNamespace(ctx, e.namespace), topic, event)
}

package infrastructure

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

// LiveDropCounter records the live frames readers lost because they fell
// behind, as the counter streamgateway.live.frames.dropped by frame kind.
type LiveDropCounter struct {
	dropped metric.Int64Counter
}

var _ query.LiveDropRecorder = (*LiveDropCounter)(nil)

func NewLiveDropCounter(provider metric.MeterProvider) (*LiveDropCounter, error) {
	dropped, err := provider.Meter("streamgateway").Int64Counter("streamgateway.live.frames.dropped",
		metric.WithDescription("Live frames a reader lost because it fell behind"), metric.WithUnit("{frame}"))
	if err != nil {
		return nil, err
	}
	return &LiveDropCounter{dropped: dropped}, nil
}

func (c *LiveDropCounter) Dropped(kind string) {
	c.dropped.Add(context.Background(), 1, metric.WithAttributes(attribute.String("kind", kind)))
}

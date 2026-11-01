// Package pubsub publishes document events to the Pub/Sub topic consumed by
// education services.
package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	gpubsub "cloud.google.com/go/pubsub/v2"

	"github.com/avi-p/yesbook/internal/document"
)

type Publisher struct {
	publisher *gpubsub.Publisher
}

// New publishes with the job ID as ordering key, so consumers see the
// events of one job in commit order. The subscription must enable message
// ordering for that to hold on delivery.
func New(client *gpubsub.Client, topic string) *Publisher {
	p := client.Publisher(topic)
	p.EnableMessageOrdering = true
	return &Publisher{publisher: p}
}

// Publish waits for the server acknowledgement. The payload carries IDs and
// status only.
func (p *Publisher) Publish(ctx context.Context, ev document.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	res := p.publisher.Publish(ctx, &gpubsub.Message{
		Data:        data,
		OrderingKey: ev.JobID,
		Attributes: map[string]string{
			"event_id":   ev.ID,
			"event_type": ev.Type,
			"tenant_id":  ev.TenantID,
			"job_id":     ev.JobID,
		},
	})
	if _, err := res.Get(ctx); err != nil {
		// A failed publish pauses its ordering key until resumed; the outbox
		// retries the event on the next sweep.
		p.publisher.ResumePublish(ev.JobID)
		return fmt.Errorf("publish %s: %w", ev.ID, err)
	}
	return nil
}

// Stop flushes pending messages.
func (p *Publisher) Stop() { p.publisher.Stop() }

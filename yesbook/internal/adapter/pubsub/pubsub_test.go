package pubsub_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"
	pb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/avi-p/yesbook/internal/adapter/pubsub"
	"github.com/avi-p/yesbook/internal/document"
)

func TestPublishOrderedByJob(t *testing.T) {
	ctx := context.Background()
	srv := pstest.NewServer()
	t.Cleanup(func() { _ = srv.Close() })
	topic := "projects/p/topics/document-events"
	if _, err := srv.GServer.CreateTopic(ctx, &pb.Topic{Name: topic}); err != nil {
		t.Fatal(err)
	}
	client, err := gpubsub.NewClient(ctx, "p",
		option.WithEndpoint(srv.Addr),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	p := pubsub.New(client, topic)
	defer p.Stop()
	ev := document.Event{ID: "ev-1", Type: "document.ready", JobID: "job-1", TenantID: "tenant-a", VersionID: "v1",
		Status: document.StatusReady, OccurredAt: time.Now().UTC()}
	if err := p.Publish(ctx, ev); err != nil {
		t.Fatal(err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.OrderingKey != "job-1" || m.Attributes["event_type"] != "document.ready" || m.Attributes["tenant_id"] != "tenant-a" {
		t.Errorf("message = %+v", m)
	}
	var got document.Event
	if err := json.Unmarshal(m.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "ev-1" || got.VersionID != "v1" {
		t.Errorf("payload = %+v", got)
	}
}

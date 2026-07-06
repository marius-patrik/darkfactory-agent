package queue

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPGQueueLive(t *testing.T) {
	dsn := os.Getenv("ROMMIE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ROMMIE_TEST_PG_DSN unset; skipping live Postgres queue test")
	}
	ctx := context.Background()
	q, err := NewPG(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	id, err := q.Enqueue(ctx, Job{Kind: "test", Payload: []byte(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	job, err := q.Dequeue(ctx, "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != id || job.Attempts != 1 || job.ClaimedBy != "worker-a" {
		t.Fatalf("unexpected job: %#v", job)
	}
	counts, err := q.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Claimed == 0 {
		t.Fatalf("expected claimed count, got %#v", counts)
	}
	if err := q.Ack(ctx, id); err != nil {
		t.Fatal(err)
	}
}

package cli

import (
	"context"
	"testing"

	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
)

type fakeCLIDelivery struct {
	requests []notifydelivery.Request
	removed  []notify.Notification
	result   notifydelivery.Result
	status   notifydelivery.Status
	observed notify.ResolvedConfig
}

func (f *fakeCLIDelivery) Deliver(_ context.Context, request notifydelivery.Request) notifydelivery.Result {
	f.requests = append(f.requests, request)
	return f.result
}
func (f *fakeCLIDelivery) Remove(_ context.Context, n notify.Notification) error {
	f.removed = append(f.removed, n)
	return nil
}

func (f *fakeCLIDelivery) Status(context.Context) notifydelivery.Status {
	f.observed = notify.CurrentConfig()
	return f.status
}

func TestNotifyPostFallbackOffersCreatedRecordToSharedDelivery(t *testing.T) {
	env, _, errOut := notifyEnv(t)
	delivery := &fakeCLIDelivery{}
	env.NotificationDelivery = delivery
	if code := runNotifyPost(env, []string{"--source", "agent", "fallback delivery"}); code != 0 {
		t.Fatalf("post=%d stderr=%q", code, errOut.String())
	}
	if len(delivery.requests) != 1 || delivery.requests[0].Discovered || delivery.requests[0].Notification.Title != "fallback delivery" {
		t.Fatalf("delivery requests = %+v", delivery.requests)
	}
}

func TestNotifyDismissFallbackUsesSharedCancellationAndRemoval(t *testing.T) {
	env, _, errOut := notifyEnv(t)
	delivery := &fakeCLIDelivery{}
	env.NotificationDelivery = delivery
	if code := runNotifyPost(env, []string{"--expiry", "sticky", "offline wait"}); code != 0 {
		t.Fatalf("post=%d stderr=%q", code, errOut.String())
	}
	if len(delivery.requests) != 1 {
		t.Fatalf("post requests = %+v", delivery.requests)
	}
	id := delivery.requests[0].Notification.ID
	if code := runNotifyDismiss(env, []string{id}); code != 0 {
		t.Fatalf("dismiss=%d stderr=%q", code, errOut.String())
	}
	if len(delivery.removed) != 1 || delivery.removed[0].ID != id {
		t.Fatalf("offline removal = %+v", delivery.removed)
	}
}

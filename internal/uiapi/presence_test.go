package uiapi

import "testing"

func TestStatusTracksPresenceAndHolderLabels(t *testing.T) {
	c := &trackedClient{}
	c.observe([]byte(`{"version":0,"type":"opened","attachment_handle":"a"}`))
	c.observe([]byte(`{"version":0,"type":"presence","control":true}`))
	c.observe([]byte(`{"version":0,"type":"holder","holder":{"kind":"ios","label":"iPhone"}}`))
	if !c.term.Control || c.term.Holder == nil || c.term.Holder.Label != "iPhone" {
		t.Fatalf("status=%+v", c.term)
	}
	c.observe([]byte(`{"version":0,"type":"presence"}`))
	c.observe([]byte(`{"version":0,"type":"holder","holder":{"kind":"","label":""}}`))
	if c.term.Control || c.term.Holder == nil || c.term.Holder.Label != "" {
		t.Fatalf("blur=%+v", c.term)
	}
	c.observe([]byte(`{"version":0,"type":"closed"}`))
	if c.open || c.term.Holder != nil {
		t.Fatalf("closed=%+v", c.term)
	}
}

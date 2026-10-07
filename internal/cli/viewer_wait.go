package cli

import (
	"github.com/marcus/sidecar/internal/uirequest"
	"time"
)

// Pin the relay destination and make its displayed expiry match this caller's wait.
func prepareViewerRequest(dir string, req *uirequest.Request, wait time.Duration) {
	if v, ok := uirequest.ReadAPIViewer(dir, time.Now()); ok && v.Focused && v.HasCapability(uirequest.APIViewerRelay) && req.Origin.HostID == "" {
		req.Viewer = v.Instance
		if wait > 0 {
			req.TTLMs = int(wait / time.Millisecond)
		}
	}
}

func finishViewerWait(dir string, req uirequest.Request, acks []uirequest.Ack) []uirequest.Ack {
	_ = uirequest.WithRequestLock(dir, req.ID, req.Action, func() error {
		// The final read and cancellation are ordered with the server's commit+ack.
		if len(acks) == 0 {
			acks, _ = uirequest.ReadAcks(dir, req.ID, req.Action)
		}
		return uirequest.Cleanup(dir, req.ID, req.Action)
	})
	return acks
}

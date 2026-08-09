package tty

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// This file implements the byte-fed screen model's bootstrap contract on top of
// the control client's single ordered event stream (slice 1 of
// docs/plans/active/td-64c916-byte-fed-tmux-screen-model.md).
//
// Nothing here runs unless a subscriber sets ControlRequest.OnModelFrame. With
// that hook nil the control client issues no extra tmux commands, builds no
// model, and delivers exactly the ControlSnapshot sequence it always did.

// ResyncReason says why a pane model needs a fresh seed, or why it stopped.
type ResyncReason uint8

// Resync reasons.
const (
	// ResyncFirstSeed is the initial bootstrap: first subscribe, Sidecar
	// restart, or a pane switch that created a new subscription.
	ResyncFirstSeed ResyncReason = iota + 1
	// ResyncReconnect is a replacement control client for the same session.
	ResyncReconnect
	// ResyncPause is %pause, or the %continue that follows it. tmux drops the
	// pane's buffered output while paused, so continuity is broken either way.
	ResyncPause
	// ResyncDiscarded is growth in the control client's client_discarded byte
	// counter: tmux dropped output because this client fell behind.
	ResyncDiscarded
	// ResyncLayout is a layout or window-pane change.
	ResyncLayout
	// ResyncResize is a Sidecar-initiated geometry change. tmux is resized
	// first and the seed reads back the authoritative resulting geometry.
	ResyncResize
	// ResyncSeedRace means pane bytes arrived between the seed transaction's
	// metadata response and its capture response, so the metadata describes an
	// older moment than the capture. Never observed against tmux 3.6b; it is
	// detected rather than assumed away.
	ResyncSeedRace
	// ResyncPaneIdentity means tmux reported a different pane id than the one
	// subscribed to. Terminal for the feed.
	ResyncPaneIdentity
	// ResyncModelFault is a malformed payload, parser error, impossible
	// geometry, or model panic. Terminal for the feed.
	ResyncModelFault
)

func (r ResyncReason) String() string {
	switch r {
	case ResyncFirstSeed:
		return "first-seed"
	case ResyncReconnect:
		return "reconnect"
	case ResyncPause:
		return "pause"
	case ResyncDiscarded:
		return "discarded"
	case ResyncLayout:
		return "layout"
	case ResyncResize:
		return "resize"
	case ResyncSeedRace:
		return "seed-race"
	case ResyncPaneIdentity:
		return "pane-identity"
	case ResyncModelFault:
		return "model-fault"
	default:
		return "unknown"
	}
}

// ModelFrame is one rendered byte-fed frame for a subscription that opted in.
// It is delivered only after a seed and its post-seed replay have completed.
type ModelFrame struct {
	Session    string
	Pane       string
	Generation uint64
	// Seeds counts completed seed transactions for this subscription. A frame
	// whose Seeds differs from the previous frame's follows a resynchronization.
	Seeds int
	Frame screenmodel.Frame

	// Discarded is the last client_discarded value observed for this control
	// client, and DiscardCheckedAt is when it was observed. tmux offers no
	// notification for the counter, so it is only known at a seed and on the
	// discardProbeInterval cadence: between the last check and now, tmux may have
	// dropped bytes for this client and this frame would be built from an
	// incomplete stream with nothing in the frame to say so.
	//
	// These two fields are what makes that window discriminable rather than
	// invisible. Any consumer comparing this frame against another source (the
	// slice-2 shadow comparison) must treat a mismatch observed later than
	// DiscardCheckedAt as unattributable until the next check confirms the
	// counter did not move. See §6 of the slice-1 evidence.
	Discarded        int64
	DiscardCheckedAt time.Time
}

// ModelInvalidation reports that a pane model lost authority. Terminal
// invalidations end the model for that subscription; the consumer stays on the
// existing capture/polling path, which slice 1 never leaves in the first place.
type ModelInvalidation struct {
	Session    string
	Pane       string
	Generation uint64
	Reason     ResyncReason
	Terminal   bool
	Err        error
}

// modelState is the pane model lifecycle from the plan's §3.
type modelState uint8

const (
	// modelIdle: a seed is required and has not been issued yet.
	modelIdle modelState = iota
	// modelSeeding: the seed transaction is in flight. Pane bytes received in
	// this state are provably already in the capture and are discarded.
	modelSeeding
	// modelLive: seeded; every pane byte is written to the model in receive
	// order.
	modelLive
	// modelFailed: terminal. The subscription is back on capture only.
	modelFailed
)

// paneModelFeed is one subscription's byte-fed model. Every field is owned by
// the control client's ordered actor goroutine; nothing else may touch it.
type paneModelFeed struct {
	id         uint64
	generation uint64
	session    string
	pane       string
	scrollback int

	model *screenmodel.Model
	state modelState

	// seedPending records a resync requested while a seed was already in
	// flight, so the reason is not lost when the in-flight seed completes.
	seedPending bool
	pendingWhy  ResyncReason

	// metaLines holds the seed transaction's metadata response until its
	// capture response arrives. rawDuringMeta counts pane bytes observed
	// between the two, which is the seed-race detector.
	metaLines     []string
	metaSeen      bool
	rawDuringMeta int
	seedRaces     int

	seeds      int
	frameDirty bool
	// discarded is the last client_discarded value seen for this client, and
	// discardCheckedAt is when it was seen. Published on every frame so a
	// consumer can tell a frame whose byte stream is confirmed complete from one
	// published inside the unobserved window between checks.
	discarded        int64
	discardSeen      bool
	discardCheckedAt time.Time

	// pendingSince is when the first byte arrived that has not yet reached a
	// published frame. It is the model path's output-to-frame latency clock,
	// started at the same event as the capture path's (paneCompareState).
	pendingSince time.Time
}

func (f *paneModelFeed) close() {
	if f.model != nil {
		f.model.Close()
		f.model = nil
	}
	f.state = modelFailed
}

// seedMetadataFormat asks for everything a seed needs in one tmux format.
//
// alternate_on, the individual mouse flags, and client_discarded are the fields
// slice 1 adds to the metadata the capture path already collected. pane_id is
// included so a pane that was replaced under the same target is detected rather
// than silently seeded from a stranger's screen.
//
// Two tracking modes have no tmux format at all: DECSET 9 (X10) and DECSET 1001
// (highlight). They cannot be seeded and are left off; the model learns them
// from bytes if the application re-enables them. Recorded as a seed gap.
const seedMetadataFormat = "#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}," +
	"#{history_size},#{alternate_on},#{mouse_standard_flag},#{mouse_button_flag}," +
	"#{mouse_all_flag},#{mouse_sgr_flag},#{client_discarded},#{pane_id}"

func buildSeedCommands(pane string, scrollback int) (metadata, capture string, err error) {
	if !controlPanePattern.MatchString(pane) {
		return "", "", fmt.Errorf("tmux control: invalid pane %q", pane)
	}
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	metadata = "display-message -p -t " + pane + " '" + seedMetadataFormat + "'"
	capture = "capture-pane -p -e -S -" + strconv.Itoa(scrollback) + " -t " + pane
	return metadata, capture, nil
}

// seedMetadata is the parsed metadata half of a seed transaction.
type seedMetadata struct {
	CursorCol     int
	CursorRow     int
	CursorVisible bool
	Height        int
	Width         int
	HistorySize   int
	AltScreen     bool
	Mouse         screenmodel.MouseState
	Discarded     int64
	PaneID        string
}

func parseSeedMetadata(line string) (seedMetadata, error) {
	parts := strings.Split(strings.TrimSpace(line), ",")
	if len(parts) != 13 {
		return seedMetadata{}, fmt.Errorf("tmux control seed: invalid metadata %q", line)
	}
	number := func(index int) (int, error) { return strconv.Atoi(parts[index]) }
	col, errCol := number(0)
	row, errRow := number(1)
	height, errHeight := number(3)
	width, errWidth := number(4)
	history, errHistory := number(5)
	if errCol != nil || errRow != nil || errHeight != nil || errWidth != nil || errHistory != nil {
		return seedMetadata{}, fmt.Errorf("tmux control seed: invalid metadata %q", line)
	}
	if width < 1 || height < 1 || history < 0 || col < 0 || row < 0 {
		return seedMetadata{}, fmt.Errorf("tmux control seed: impossible metadata %q", line)
	}
	// client_discarded is empty when the command runs outside a control client.
	// Treat that as zero rather than as a parse failure.
	discarded := int64(0)
	if parts[11] != "" {
		parsed, err := strconv.ParseInt(parts[11], 10, 64)
		if err != nil || parsed < 0 {
			return seedMetadata{}, fmt.Errorf("tmux control seed: invalid client_discarded %q", parts[11])
		}
		discarded = parsed
	}
	flag := func(index int) bool { return parts[index] != "" && parts[index] != "0" }
	return seedMetadata{
		CursorCol:     col,
		CursorRow:     row,
		CursorVisible: flag(2),
		Height:        height,
		Width:         width,
		HistorySize:   history,
		AltScreen:     flag(6),
		Mouse: screenmodel.MouseState{
			Normal:      flag(7),
			ButtonEvent: flag(8),
			AnyEvent:    flag(9),
			SGR:         flag(10),
		},
		Discarded: discarded,
		PaneID:    parts[12],
	}, nil
}

var errClientClosed = errors.New("tmux control client closed")

// ---------------------------------------------------------------------------
// Feed lifecycle. Every method below runs on the control client's ordered actor
// goroutine and nowhere else.
// ---------------------------------------------------------------------------

func (c *sessionControlClient) startModelFeed(sub managerControlSubscription) {
	if _, exists := c.models[sub.id]; exists {
		return
	}
	// The start is posted after the manager lock is released (see
	// ControlManager.activate), so the subscription can already have been
	// removed by the time this runs. Starting a feed for a dead subscription
	// would leak an emulator until the client closed.
	if !c.has(sub.id, sub.generation) {
		return
	}
	scrollback := sub.request.Scrollback
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	feed := &paneModelFeed{
		id:         sub.id,
		generation: sub.generation,
		session:    c.session,
		pane:       sub.request.Pane,
		scrollback: scrollback,
		state:      modelIdle,
	}
	c.models[sub.id] = feed
	screenCompareStats.bump(&screenCompareStats.ModelsOpened, 1)
	c.armDiscardProbe()
	c.beginSeed(feed, ResyncFirstSeed)
}

func (c *sessionControlClient) stopModelFeed(id uint64) {
	feed := c.models[id]
	if feed == nil {
		return
	}
	delete(c.models, id)
	feed.close()
	screenCompareStats.bump(&screenCompareStats.ModelsClosed, 1)
}

func (c *sessionControlClient) failAllModels(reason ResyncReason, err error) {
	for id, feed := range c.models {
		delete(c.models, id)
		c.invalidate(feed, reason, err, true)
		feed.close()
	}
}

func (c *sessionControlClient) requestSeed(id uint64, reason ResyncReason) {
	if feed := c.models[id]; feed != nil {
		c.beginSeed(feed, reason)
	}
}

func (c *sessionControlClient) requestSeedForPane(pane string, reason ResyncReason) {
	for _, feed := range c.models {
		if feed.pane == pane {
			c.beginSeed(feed, reason)
		}
	}
}

func (c *sessionControlClient) requestSeedForAll(reason ResyncReason) {
	for _, feed := range c.models {
		c.beginSeed(feed, reason)
	}
}

// beginSeed issues one seed transaction: metadata and a bounded rendered
// capture, written together so tmux executes them back to back.
//
// The barrier is the transaction's *capture response position in this ordered
// stream*. Pane bytes seen before it are provably already rendered into the
// capture and are discarded; every byte seen after it is replayed into the model
// in receive order. Nothing else is needed, because tmux never emits a
// notification inside a command block and the actor processes the stream one
// event at a time.
func (c *sessionControlClient) beginSeed(feed *paneModelFeed, reason ResyncReason) {
	if feed.state == modelFailed {
		return
	}
	if feed.state == modelSeeding {
		// Collapse into the in-flight seed; keep the earliest reason so the
		// consumer still learns why continuity was broken.
		feed.seedPending = true
		if feed.pendingWhy == 0 {
			feed.pendingWhy = reason
		}
		return
	}
	metadata, capture, err := buildSeedCommands(feed.pane, feed.scrollback)
	if err != nil {
		c.faultFeed(feed, ResyncModelFault, err)
		return
	}
	feed.state = modelSeeding
	feed.metaLines = nil
	feed.metaSeen = false
	feed.rawDuringMeta = 0
	feed.seedPending = false
	feed.pendingWhy = 0
	id, generation := feed.id, feed.generation

	onMetadata := func(response controlResponse) {
		c.seedMetadataResponse(id, generation, response)
	}
	onCapture := func(response controlResponse) {
		c.seedCaptureResponse(id, generation, reason, response)
	}
	screenCompareStats.bump(&screenCompareStats.SeedCaptures, 1)
	if err := c.channel.SendPair(metadata, capture, onMetadata, onCapture); err != nil {
		c.faultFeed(feed, ResyncModelFault, fmt.Errorf("tmux control seed write: %w", err))
	}
}

func (c *sessionControlClient) liveFeed(id, generation uint64) *paneModelFeed {
	feed := c.models[id]
	if feed == nil || feed.generation != generation || feed.state == modelFailed {
		return nil
	}
	return feed
}

func (c *sessionControlClient) seedMetadataResponse(id, generation uint64, response controlResponse) {
	feed := c.liveFeed(id, generation)
	if feed == nil || feed.state != modelSeeding {
		return
	}
	if response.Err != nil {
		c.faultFeed(feed, ResyncModelFault, response.Err)
		return
	}
	feed.metaLines = response.Lines
	feed.metaSeen = true
	feed.rawDuringMeta = 0
}

func (c *sessionControlClient) seedCaptureResponse(id, generation uint64, reason ResyncReason, response controlResponse) {
	feed := c.liveFeed(id, generation)
	if feed == nil || feed.state != modelSeeding {
		return
	}
	if response.Err != nil {
		c.faultFeed(feed, ResyncModelFault, response.Err)
		return
	}
	if !feed.metaSeen {
		c.faultFeed(feed, ResyncModelFault, errors.New("tmux control seed: capture without metadata"))
		return
	}
	if feed.rawDuringMeta > 0 {
		// Pane bytes landed between the two halves of the transaction, so the
		// metadata cursor describes an older screen than the capture. Those
		// bytes are in the capture and are correctly discarded; only the cursor
		// would be stale, and a stale cursor corrupts every replayed byte after
		// it. Reseed instead. This has never been observed against tmux 3.6b.
		feed.seedRaces++
		feed.state = modelIdle
		c.invalidate(feed, ResyncSeedRace, nil, false)
		c.beginSeed(feed, ResyncSeedRace)
		return
	}

	seed, meta, err := seedFromResponses(feed.metaLines, response.Lines, feed.scrollback)
	feed.metaLines = nil
	feed.metaSeen = false
	if err != nil {
		c.faultFeed(feed, ResyncModelFault, err)
		return
	}
	if meta.PaneID != "" && meta.PaneID != feed.pane {
		c.faultFeed(feed, ResyncPaneIdentity,
			fmt.Errorf("tmux control seed: pane %s reported as %s", feed.pane, meta.PaneID))
		return
	}
	if feed.discardSeen && meta.Discarded > feed.discarded {
		// tmux dropped output for this client at some point before the seed.
		// The seed itself is the recovery, so record the new baseline and
		// continue.
		c.invalidate(feed, ResyncDiscarded, nil, false)
	}
	feed.discarded = meta.Discarded
	feed.discardSeen = true
	feed.discardCheckedAt = time.Now()

	if feed.model == nil {
		feed.model = screenmodel.New(seed.Width, seed.Height)
	}
	if err := feed.model.Seed(seed); err != nil {
		c.faultFeed(feed, ResyncModelFault, err)
		return
	}
	feed.state = modelLive
	feed.seeds++
	feed.frameDirty = true
	feed.pendingSince = time.Time{}
	screenCompareStats.recordSeed(reason)
	if reason != ResyncFirstSeed || feed.seeds > 1 {
		c.invalidate(feed, reason, nil, false)
	}
	if feed.seedPending {
		why := feed.pendingWhy
		feed.seedPending = false
		feed.pendingWhy = 0
		c.beginSeed(feed, why)
		return
	}
	c.armModelTick()
}

// feedModels writes one %output notification into every model on that pane.
// The payload is decoded at most once, and only when a model exists — with the
// model path off this is a nil map lookup and nothing else.
func (c *sessionControlClient) feedModels(event controlEvent) {
	if len(c.models) == 0 {
		return
	}
	var decoded []byte
	for _, feed := range c.models {
		if feed.pane != event.Pane {
			continue
		}
		switch feed.state {
		case modelSeeding:
			// Provably already in the capture this seed is waiting for.
			if feed.metaSeen {
				feed.rawDuringMeta++
			}
		case modelLive:
			if decoded == nil {
				decoded = event.DecodedPayload()
			}
			if len(decoded) == 0 {
				continue
			}
			started := time.Now()
			if err := feed.model.Write(decoded); err != nil {
				c.faultFeed(feed, ResyncModelFault, err)
				continue
			}
			if ScreenCompareEnabled() {
				screenCompareStats.recordRaw(len(decoded))
				screenCompareStats.mu.Lock()
				screenCompareStats.ModelWriteUS.add(time.Since(started))
				screenCompareStats.mu.Unlock()
				if feed.pendingSince.IsZero() {
					feed.pendingSince = started
				}
			}
			feed.frameDirty = true
			c.armModelTick()
		case modelIdle, modelFailed:
		}
	}
}

// armModelTick schedules one coalesced frame publication back onto the actor.
func (c *sessionControlClient) armModelTick() {
	if c.modelTimer {
		return
	}
	c.modelTimer = true
	time.AfterFunc(c.coalesce, func() {
		select {
		case c.modelTick <- struct{}{}:
		case <-c.quit:
		}
	})
}

func (c *sessionControlClient) publishModelFrames() {
	c.modelTimer = false
	for _, feed := range c.models {
		if feed.state != modelLive || !feed.frameDirty {
			continue
		}
		rendered := time.Now()
		frame, err := feed.model.Frame()
		if err != nil {
			c.faultFeed(feed, ResyncModelFault, err)
			continue
		}
		if ScreenCompareEnabled() {
			now := time.Now()
			screenCompareStats.mu.Lock()
			screenCompareStats.ModelFrames++
			screenCompareStats.ModelRenderUS.add(now.Sub(rendered))
			if !feed.pendingSince.IsZero() {
				screenCompareStats.OutputToFrameUS.add(now.Sub(feed.pendingSince))
			}
			screenCompareStats.mu.Unlock()
			feed.pendingSince = time.Time{}
			screenCompareStats.recordModelBytes(feed.model.Footprint())
		}
		feed.frameDirty = false
		payload := ModelFrame{
			Session:          feed.session,
			Pane:             feed.pane,
			Generation:       feed.generation,
			Seeds:            feed.seeds,
			Frame:            frame,
			Discarded:        feed.discarded,
			DiscardCheckedAt: feed.discardCheckedAt,
		}
		c.mu.Lock()
		sub, ok := c.subs[feed.id]
		valid := ok && !c.closed && sub.generation == feed.generation
		callback := sub.request.OnModelFrame
		gate := sub.delivery
		c.mu.Unlock()
		if !valid || callback == nil {
			continue
		}
		gate.invoke(func() { callback(payload) })
	}
}

// faultFeed invalidates exactly one pane model. It never touches the control
// reader, the capture path, or any other subscription.
func (c *sessionControlClient) faultFeed(feed *paneModelFeed, reason ResyncReason, err error) {
	screenCompareStats.bump(&screenCompareStats.Faults, 1)
	delete(c.models, feed.id)
	c.invalidate(feed, reason, err, true)
	feed.close()
}

func (c *sessionControlClient) invalidate(feed *paneModelFeed, reason ResyncReason, err error, terminal bool) {
	c.mu.Lock()
	sub, ok := c.subs[feed.id]
	valid := ok && !c.closed && sub.generation == feed.generation
	callback := sub.request.OnModelInvalid
	gate := sub.delivery
	c.mu.Unlock()
	if !valid || callback == nil {
		return
	}
	payload := ModelInvalidation{
		Session:    feed.session,
		Pane:       feed.pane,
		Generation: feed.generation,
		Reason:     reason,
		Terminal:   terminal,
		Err:        err,
	}
	gate.invoke(func() { callback(payload) })
}

// armDiscardProbe keeps one low-frequency client_discarded query in flight
// while any model is live. It is deliberately not per-burst.
func (c *sessionControlClient) armDiscardProbe() {
	if c.discardWait || len(c.models) == 0 {
		return
	}
	c.discardWait = true
	time.AfterFunc(discardProbeInterval, func() {
		c.post(func() {
			c.discardWait = false
			if len(c.models) == 0 {
				return
			}
			screenCompareStats.bump(&screenCompareStats.DiscardProbes, 1)
			_ = c.channel.Send("display-message -p '#{client_discarded}'", func(response controlResponse) {
				c.discardResponse(response)
			})
			c.armDiscardProbe()
		})
	})
}

func (c *sessionControlClient) discardResponse(response controlResponse) {
	if response.Err != nil || len(response.Lines) == 0 {
		return
	}
	text := strings.TrimSpace(response.Lines[0])
	if text == "" {
		return
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 {
		return
	}
	checked := time.Now()
	for _, feed := range c.models {
		// Every feed's window closes on a successful probe, whether or not the
		// counter moved: a consumer needs to know the stream was confirmed
		// complete up to this instant, not only that a loss was found.
		feed.discardCheckedAt = checked
		if !feed.discardSeen || value <= feed.discarded {
			continue
		}
		screenCompareStats.mu.Lock()
		screenCompareStats.DiscardedBytes += value - feed.discarded
		screenCompareStats.mu.Unlock()
		feed.discarded = value
		c.invalidate(feed, ResyncDiscarded, nil, false)
		c.beginSeed(feed, ResyncDiscarded)
	}
}

// seedFromResponses turns a completed seed transaction into a model seed.
func seedFromResponses(metaLines, captureLines []string, scrollback int) (screenmodel.Seed, seedMetadata, error) {
	if len(metaLines) == 0 {
		return screenmodel.Seed{}, seedMetadata{}, errors.New("tmux control seed: missing metadata response")
	}
	meta, err := parseSeedMetadata(metaLines[0])
	if err != nil {
		return screenmodel.Seed{}, seedMetadata{}, err
	}
	if scrollback <= 0 {
		scrollback = DefaultScrollbackLines
	}
	captureBase := max(meta.HistorySize-scrollback, 0)
	if meta.AltScreen {
		// tmux freezes history while an application owns the alternate screen
		// and capture returns the alternate grid only.
		captureBase = meta.HistorySize
	}
	return screenmodel.Seed{
		Output:        strings.Join(captureLines, "\n"),
		CaptureBase:   captureBase,
		HistorySize:   meta.HistorySize,
		Width:         meta.Width,
		Height:        meta.Height,
		CursorRow:     meta.CursorRow,
		CursorCol:     meta.CursorCol,
		CursorVisible: meta.CursorVisible,
		AltScreen:     meta.AltScreen,
		Mouse:         meta.Mouse,
	}, meta, nil
}

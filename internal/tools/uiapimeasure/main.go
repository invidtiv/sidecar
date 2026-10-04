// Command uiapimeasure measures the UI API v0 terminal stream for
// scripts/ui-api-measure.sh: frames and bytes per second for a set of busy
// terminals, capture-pane commands per second (from a log the script's tmux
// wrapper writes), Sidecar and tmux CPU, and keystroke-to-echo latency on one
// more terminal. It prints one JSON summary per run.
//
// Each terminal is its own WebSocket, as each <sidecar-terminal> on a page is.
// Busy terminals are view-only. The echo terminal takes control at the pane's
// current geometry (so nothing is resized), types one byte at a time into a
// pane running `cat`, and times each byte from the input request to the first
// frame that shows it.
//
// Run it by naming the package explicitly:
//
//	go run ./internal/tools/uiapimeasure -url ws://127.0.0.1:PORT/api/v0/terminal -bearer TOKEN -busy S1,S2 -window 15s
package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// stream is one terminal WebSocket with a reader that routes correlated
// responses to their caller and counts every frame.
type stream struct {
	name   string
	conn   *websocket.Conn
	number atomic.Int64

	mu      sync.Mutex
	waiters map[string]chan mobileproto.Response
	onFrame func(mobileproto.Response)
	readErr error

	counting  atomic.Bool
	frames    atomic.Int64
	messages  atomic.Int64
	wireBytes atomic.Int64
	vtBytes   atomic.Int64
	gzipBytes atomic.Int64 // each message gzipped on its own
	flateMu   sync.Mutex
	flateBuf  countingWriter
	flateW    *flate.Writer // one stream, flushed per message: permessage-deflate with context takeover
	done      chan struct{}
}

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

var compressionMode = websocket.CompressionDisabled

type wireConn struct {
	net.Conn
	stream *stream
}

func (c *wireConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.stream.counting.Load() {
		c.stream.wireBytes.Add(int64(n))
	}
	return n, err
}

func dial(ctx context.Context, name, url, bearer, origin string) (*stream, error) {
	s := &stream{name: name, waiters: map[string]chan mobileproto.Response{}, done: make(chan struct{})}
	options := &websocket.DialOptions{HTTPHeader: http.Header{}, CompressionMode: compressionMode,
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &wireConn{Conn: c, stream: s}, nil
		}}}}
	if bearer != "" {
		options.HTTPHeader.Set("Authorization", "Bearer "+bearer)
	}
	if origin != "" {
		options.HTTPHeader.Set("Origin", origin)
	}
	conn, _, err := websocket.Dial(ctx, url, options)
	if err != nil {
		return nil, fmt.Errorf("%s: dial: %w", name, err)
	}
	conn.SetReadLimit(mobileproto.MaxLineBytes)
	s.conn = conn
	s.flateW, _ = flate.NewWriter(&s.flateBuf, flate.DefaultCompression)
	go s.readLoop()
	return s, nil
}

func (s *stream) readLoop() {
	defer close(s.done)
	var gz bytes.Buffer
	for {
		kind, data, err := s.conn.Read(context.Background())
		if err != nil {
			s.mu.Lock()
			s.readErr = err
			for id, ch := range s.waiters {
				close(ch)
				delete(s.waiters, id)
			}
			s.mu.Unlock()
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		var response mobileproto.Response
		if err := json.Unmarshal(data, &response); err != nil {
			continue
		}
		if s.counting.Load() {
			s.messages.Add(1)
			// Actual socket bytes are counted by wireConn before decompression.
			gz.Reset()
			w, _ := gzip.NewWriterLevel(&gz, gzip.DefaultCompression)
			_, _ = w.Write(data)
			_ = w.Close()
			s.gzipBytes.Add(int64(gz.Len()))
			s.flateMu.Lock()
			_, _ = s.flateW.Write(data)
			_ = s.flateW.Flush()
			s.flateMu.Unlock()
			if response.Type == mobileproto.ResponseFrame {
				s.frames.Add(1)
				s.vtBytes.Add(int64(base64.StdEncoding.DecodedLen(len(response.RenderVTBase64))))
			}
		}
		if response.RequestID != "" {
			s.mu.Lock()
			ch := s.waiters[response.RequestID]
			delete(s.waiters, response.RequestID)
			s.mu.Unlock()
			if ch != nil {
				ch <- response
				continue
			}
		}
		if response.Type == mobileproto.ResponseFrame {
			s.mu.Lock()
			fn := s.onFrame
			s.mu.Unlock()
			if fn != nil {
				fn(response)
			}
		}
	}
}

func (s *stream) call(ctx context.Context, request mobileproto.Request) (mobileproto.Response, error) {
	request.Version = mobileproto.Version
	request.RequestID = s.name + "-" + strconv.FormatInt(s.number.Add(1), 10)
	ch := make(chan mobileproto.Response, 1)
	s.mu.Lock()
	if s.readErr != nil {
		err := s.readErr
		s.mu.Unlock()
		return mobileproto.Response{}, err
	}
	s.waiters[request.RequestID] = ch
	s.mu.Unlock()
	data, err := json.Marshal(request)
	if err != nil {
		return mobileproto.Response{}, err
	}
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return mobileproto.Response{}, err
	}
	select {
	case response, ok := <-ch:
		if !ok {
			return mobileproto.Response{}, fmt.Errorf("%s: stream ended during %s", s.name, request.Type)
		}
		if response.Type == mobileproto.ResponseError {
			return response, fmt.Errorf("%s: %s refused: %s: %s", s.name, request.Type, response.Error.Code, response.Error.Message)
		}
		return response, nil
	case <-ctx.Done():
		return mobileproto.Response{}, ctx.Err()
	}
}

// frameState is the latest full frame of an attachment, which every mutation
// names as its checkpoint.
type frameState struct {
	mu       sync.Mutex
	reset    uint64
	output   uint64
	geometry mobileproto.Geometry
	vt       []byte
	seen     chan struct{} // closed and replaced on every frame
}

func newFrameState() *frameState { return &frameState{seen: make(chan struct{})} }

func (f *frameState) apply(r mobileproto.Response) {
	vt, _ := base64.StdEncoding.DecodeString(r.RenderVTBase64)
	f.mu.Lock()
	f.reset, f.output, f.vt = r.ResetGeneration, r.OutputSequence, vt
	if r.Geometry != nil {
		f.geometry = *r.Geometry
	}
	close(f.seen)
	f.seen = make(chan struct{})
	f.mu.Unlock()
}

func (f *frameState) snapshot() (uint64, uint64, mobileproto.Geometry, []byte, chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reset, f.output, f.geometry, f.vt, f.seen
}

type opened struct {
	s          *stream
	attachment string
	frames     *frameState
	session    string
	pane       string
}

func open(ctx context.Context, s *stream, target string) (*opened, error) {
	if _, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestHello}); err != nil {
		return nil, err
	}
	resolved, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestResolve, Target: target})
	if err != nil {
		return nil, err
	}
	frames := newFrameState()
	_, _, _, _, first := frames.snapshot()
	s.mu.Lock()
	s.onFrame = frames.apply
	s.mu.Unlock()
	response, err := s.call(ctx, mobileproto.Request{Type: mobileproto.RequestOpen, TargetHandle: resolved.Target.Handle, AttachmentID: "measure-" + s.name})
	if err != nil {
		return nil, err
	}
	select {
	case <-first:
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: no first frame: %w", s.name, ctx.Err())
	}
	return &opened{s: s, attachment: response.AttachmentHandle, frames: frames, session: resolved.Target.Session, pane: resolved.Target.Pane}, nil
}

func (o *opened) close(ctx context.Context) {
	_, _ = o.s.call(ctx, mobileproto.Request{Type: mobileproto.RequestClose, AttachmentHandle: o.attachment})
	_ = o.s.conn.Close(websocket.StatusNormalClosure, "measurement complete")
	select {
	case <-o.s.done:
	case <-time.After(2 * time.Second):
	}
}

// latency types into the echo attachment and returns one sample per byte.
func latency(ctx context.Context, o *opened, count int, rng *rand.Rand) ([]float64, error) {
	reset, output, geometry, _, _ := o.frames.snapshot()
	op := uint64(0)
	next := func() uint64 { op++; return op }
	control, err := o.s.call(ctx, mobileproto.Request{Type: mobileproto.RequestControl, AttachmentHandle: o.attachment, OperationSequence: next(),
		LastResetGeneration: reset, LastOutputSequence: output, Columns: geometry.Columns, Rows: geometry.Rows})
	if err != nil {
		return nil, err
	}
	// Wait for a frame in the control response's reset generation.
	for {
		r, _, _, _, seen := o.frames.snapshot()
		if r == control.ResetGeneration {
			break
		}
		select {
		case <-seen:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	send := func(data string) error {
		r, out, _, _, _ := o.frames.snapshot()
		_, err := o.s.call(ctx, mobileproto.Request{Type: mobileproto.RequestInput, AttachmentHandle: o.attachment, OperationSequence: next(),
			LastResetGeneration: r, LastOutputSequence: out, DataBase64: base64.StdEncoding.EncodeToString([]byte(data))})
		return err
	}
	waitFor := func(text string) error {
		for {
			_, _, _, vt, seen := o.frames.snapshot()
			if bytes.Contains(vt, []byte(text)) {
				return nil
			}
			select {
			case <-seen:
			case <-ctx.Done():
				return fmt.Errorf("echo of %q never arrived: %w", text, ctx.Err())
			}
		}
	}
	const letters = "abcdefghijklmnopqrstuvwxyz"
	const perLine = 24
	samples := make([]float64, 0, count)
	for segment := 0; len(samples) < count; segment++ {
		// A tag no earlier line shares, sent unmeasured, so containment of the
		// growing line proves the newest byte arrived.
		line := "Q" + string(letters[segment/26%26]) + string(letters[segment%26]) + "_"
		if err := send(line); err != nil {
			return samples, err
		}
		if err := waitFor(line); err != nil {
			return samples, err
		}
		for i := 0; i < perLine && len(samples) < count; i++ {
			time.Sleep(time.Duration(20+rng.IntN(40)) * time.Millisecond)
			b := string(letters[rng.IntN(len(letters))])
			line += b
			started := time.Now()
			if err := send(b); err != nil {
				return samples, err
			}
			if err := waitFor(line); err != nil {
				return samples, err
			}
			samples = append(samples, float64(time.Since(started).Microseconds())/1000)
		}
		if err := send("\x15"); err != nil { // ^U: kill the line in cat's canonical mode
			return samples, err
		}
	}
	r, out, _, _, _ := o.frames.snapshot()
	_, _ = o.s.call(ctx, mobileproto.Request{Type: mobileproto.RequestRelease, AttachmentHandle: o.attachment, OperationSequence: next(),
		LastResetGeneration: r, LastOutputSequence: out})
	return samples, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := p / 100 * float64(len(sorted)-1)
	lo, hi := int(math.Floor(rank)), int(math.Ceil(rank))
	return round(sorted[lo] + (sorted[hi]-sorted[lo])*(rank-float64(lo)))
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

// cpuSeconds reads cumulative CPU time for the given PIDs from ps.
func cpuSeconds(pids []int) float64 {
	if len(pids) == 0 {
		return 0
	}
	args := []string{"-o", "time="}
	for _, pid := range pids {
		args = append(args, "-p", strconv.Itoa(pid))
	}
	out, _ := exec.Command("ps", args...).Output()
	total := 0.0
	for _, field := range strings.Fields(string(out)) {
		// [[dd-]hh:]mm:ss.cc
		parts := strings.Split(field, ":")
		mult := 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			part := parts[i]
			if i == 0 && strings.Contains(part, "-") {
				dh := strings.SplitN(part, "-", 2)
				days, _ := strconv.ParseFloat(dh[0], 64)
				total += days * 86400
				part = dh[1]
			}
			v, _ := strconv.ParseFloat(part, 64)
			total += v * mult
			mult *= 60
		}
	}
	return total
}

// tmuxClients finds the tmux control-mode clients serving Sidecar, which run
// beneath the serve process (through the counting wrapper when it is used).
func tmuxClients(parent int) []int {
	out, _ := exec.Command("ps", "-axo", "pid=,ppid=,comm=").Output()
	children := map[int][]int{}
	comm := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		children[ppid] = append(children[ppid], pid)
		comm[pid] = f[2]
	}
	var found []int
	var walk func(int)
	walk = func(p int) {
		for _, c := range children[p] {
			if strings.HasSuffix(comm[c], "tmux") {
				found = append(found, c)
			}
			walk(c)
		}
	}
	walk(parent)
	return found
}

func countIn(path, needle string) int64 {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return int64(bytes.Count(data, []byte(needle)))
}

func lineCount(path string) int64 {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return int64(bytes.Count(data, []byte("\n")))
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func run() error {
	compression := flag.Bool("compression", false, "negotiate permessage-deflate with context takeover")
	label := flag.String("label", "", "scenario label for the summary")
	wsURL := flag.String("url", "", "WebSocket URL of /api/v0/terminal")
	bearer := flag.String("bearer", "", "bearer token for the upgrade (sent with no Origin, as Node does)")
	origin := flag.String("origin", "", "Origin header, if any")
	busyList := flag.String("busy", "", "comma-separated busy targets, each attached view-only on its own WebSocket")
	echoTarget := flag.String("echo", "", "target running `cat` for keystroke-to-echo latency")
	keystrokes := flag.Int("keystrokes", 200, "measured keystrokes on -echo")
	warmup := flag.Duration("warmup", 2*time.Second, "time after attaching before the window opens")
	window := flag.Duration("window", 15*time.Second, "throughput measurement window")
	controlLog := flag.String("control-log", "", "log of commands written to tmux control clients")
	spawnLog := flag.String("spawn-log", "", "log of tmux process spawns")
	serverPID := flag.Int("pid", 0, "sidecar api serve PID")
	tmuxPID := flag.Int("tmux-pid", 0, "private tmux server PID")
	timeout := flag.Duration("timeout", 90*time.Second, "overall deadline")
	flag.Parse()
	if *compression {
		compressionMode = websocket.CompressionContextTakeover
	}
	if *wsURL == "" {
		return errors.New("-url is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	rng := rand.New(rand.NewPCG(1, 2))

	var busy []*opened
	for i, target := range splitList(*busyList) {
		s, err := dial(ctx, "busy"+strconv.Itoa(i+1), *wsURL, *bearer, *origin)
		if err != nil {
			return err
		}
		o, err := open(ctx, s, target)
		if err != nil {
			return err
		}
		busy = append(busy, o)
	}
	var echo *opened
	if *echoTarget != "" {
		s, err := dial(ctx, "echo", *wsURL, *bearer, *origin)
		if err != nil {
			return err
		}
		if echo, err = open(ctx, s, *echoTarget); err != nil {
			return err
		}
	}
	summary := map[string]any{"compression": *compression, "wire_measurement": "socket bytes before decompression", "label": *label, "busy_terminals": len(busy), "echo_terminal": echo != nil}
	if len(busy) > 0 {
		_, _, g, _, _ := busy[0].frames.snapshot()
		summary["geometry"] = fmt.Sprintf("%dx%d", g.Columns, g.Rows)
	} else if echo != nil {
		_, _, g, _, _ := echo.frames.snapshot()
		summary["geometry"] = fmt.Sprintf("%dx%d", g.Columns, g.Rows)
	}

	time.Sleep(*warmup)
	if *window > 0 {
		clients := tmuxClients(*serverPID)
		sidecarCPU0, tmuxCPU0, clientCPU0 := cpuSeconds([]int{*serverPID}), cpuSeconds([]int{*tmuxPID}), cpuSeconds(clients)
		captures0, spawns0, commands0 := countIn(*controlLog, "capture-pane"), lineCount(*spawnLog), lineCount(*controlLog)
		for _, o := range busy {
			o.s.counting.Store(true)
		}
		started := time.Now()
		time.Sleep(*window)
		for _, o := range busy {
			o.s.counting.Store(false)
		}
		elapsed := time.Since(started).Seconds()
		sidecarCPU, tmuxCPU, clientCPU := cpuSeconds([]int{*serverPID})-sidecarCPU0, cpuSeconds([]int{*tmuxPID})-tmuxCPU0, cpuSeconds(clients)-clientCPU0
		captures, spawns, commands := countIn(*controlLog, "capture-pane")-captures0, lineCount(*spawnLog)-spawns0, lineCount(*controlLog)-commands0

		var per []map[string]any
		var frames, wire, vt, gz, fl int64
		for _, o := range busy {
			o.s.flateMu.Lock()
			flated := o.s.flateBuf.n
			o.s.flateMu.Unlock()
			f, w, v, g := o.s.frames.Load(), o.s.wireBytes.Load(), o.s.vtBytes.Load(), o.s.gzipBytes.Load()
			frames, wire, vt, gz, fl = frames+f, wire+w, vt+v, gz+g, fl+flated
			entry := map[string]any{"name": o.s.name, "session": o.session, "frames_per_s": round(float64(f) / elapsed),
				"wire_kb_per_s": round(float64(w) / elapsed / 1024), "avg_frame_kb": 0.0}
			if f > 0 {
				entry["avg_frame_kb"] = round(float64(w) / float64(f) / 1024)
			}
			o.s.mu.Lock()
			if o.s.readErr != nil {
				entry["stream_error"] = o.s.readErr.Error()
			}
			o.s.mu.Unlock()
			per = append(per, entry)
		}
		summary["window_s"] = round(elapsed)
		summary["terminals"] = per
		summary["frames_per_s"] = round(float64(frames) / elapsed)
		summary["wire_kb_per_s"] = round(float64(wire) / elapsed / 1024)
		summary["vt_payload_kb_per_s"] = round(float64(vt) / elapsed / 1024)
		summary["gzip_per_message_kb_per_s"] = round(float64(gz) / elapsed / 1024)
		summary["deflate_stream_kb_per_s"] = round(float64(fl) / elapsed / 1024)
		if *controlLog != "" {
			summary["capture_pane_per_s"] = round(float64(captures) / elapsed)
			summary["control_commands_per_s"] = round(float64(commands) / elapsed)
		}
		if *spawnLog != "" {
			summary["tmux_spawns_per_s"] = round(float64(spawns) / elapsed)
		}
		if *serverPID > 0 {
			summary["sidecar_cpu_pct"] = round(100 * sidecarCPU / elapsed)
			summary["tmux_control_clients"] = len(clients)
			summary["tmux_control_clients_cpu_pct"] = round(100 * clientCPU / elapsed)
		}
		if *tmuxPID > 0 {
			summary["tmux_server_cpu_pct"] = round(100 * tmuxCPU / elapsed)
		}
	}
	if echo != nil {
		samples, err := latency(ctx, echo, *keystrokes, rng)
		if err != nil {
			return fmt.Errorf("latency after %d samples: %w", len(samples), err)
		}
		sorted := append([]float64(nil), samples...)
		sort.Float64s(sorted)
		mean := 0.0
		for _, v := range samples {
			mean += v
		}
		summary["keystroke_echo_ms"] = map[string]any{"n": len(samples), "p50": percentile(sorted, 50), "p95": percentile(sorted, 95),
			"p99": percentile(sorted, 99), "max": round(sorted[len(sorted)-1]), "min": round(sorted[0]), "mean": round(mean / float64(len(samples)))}
	}
	for _, o := range busy {
		o.s.mu.Lock()
		err := o.s.readErr
		o.s.mu.Unlock()
		if err != nil {
			summary["error"] = fmt.Sprintf("%s ended early: %v", o.s.name, err)
		}
	}
	for _, o := range append(busy, echo) {
		if o != nil {
			o.close(ctx)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "uiapimeasure:", err)
		os.Exit(1)
	}
}

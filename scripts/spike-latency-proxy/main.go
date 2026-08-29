// Command spike-latency-proxy inserts a known, symmetric delay in front of a
// TCP service so the remote-host spike can be measured at WAN round trips
// without a WAN.
//
// It exists because the honest LAN and Tailscale numbers turned out to be the
// same number: Tailscale negotiated a direct path over the same local network,
// so the "VPN hop" measured 8ms like everything else. Two identical columns
// answer nothing about how a proxied control-mode pane behaves at 60 or 150ms,
// which is the range that actually decides whether this feature feels local.
//
// A deterministic shim is also better evidence than a real WAN would be: the
// number is reproducible, so a later Herdr bake-off compares against the same
// conditions rather than against whatever the internet was doing that day.
//
// It adds latency only. It does not model jitter, loss, or bandwidth limits —
// naming that plainly matters, because a reader who assumes otherwise will
// over-trust the throughput column.
//
//	go run ./scripts/spike-latency-proxy -listen 127.0.0.1:2222 -target marcusbook-pro.local:22 -rtt 150ms
//
// Then point ssh at the listener. Each direction is delayed by half the
// configured RTT, so a request/response pair pays it once.
package main

import (
	"flag"
	"io"
	"log"
	"net"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:2222", "local address to listen on")
	target := flag.String("target", "", "host:port to forward to")
	rtt := flag.Duration("rtt", 100*time.Millisecond, "round-trip latency to add")
	flag.Parse()

	if *target == "" {
		log.Fatal("-target is required")
	}
	oneWay := *rtt / 2

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("latency proxy: %s -> %s (+%v RTT)", *listen, *target, *rtt)

	for {
		client, err := listener.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go serve(client, *target, oneWay)
	}
}

func serve(client net.Conn, target string, oneWay time.Duration) {
	defer func() { _ = client.Close() }()
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("dial %s: %v", target, err)
		return
	}
	defer func() { _ = upstream.Close() }()

	done := make(chan struct{}, 2)
	go pipe(upstream, client, oneWay, done)
	go pipe(client, upstream, oneWay, done)
	<-done
}

// pipe copies src to dst, holding each chunk for the one-way delay.
//
// The delay is applied per chunk with a deadline captured at read time, not as
// a sleep between writes. Sleeping between writes would serialise the stream
// and turn a latency shim into a bandwidth limiter — a bulk transfer would
// creep along at one buffer per delay period and the throughput column would
// be measuring this program rather than the link.
func pipe(dst, src net.Conn, delay time.Duration, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	type chunk struct {
		data []byte
		at   time.Time
	}
	queue := make(chan chunk, 1024)

	go func() {
		defer close(queue)
		buffer := make([]byte, 64<<10)
		for {
			n, err := src.Read(buffer)
			if n > 0 {
				payload := make([]byte, n)
				copy(payload, buffer[:n])
				queue <- chunk{data: payload, at: time.Now().Add(delay)}
			}
			if err != nil {
				return
			}
		}
	}()

	for c := range queue {
		if wait := time.Until(c.at); wait > 0 {
			time.Sleep(wait)
		}
		if _, err := dst.Write(c.data); err != nil {
			_, _ = io.Copy(io.Discard, src)
			return
		}
	}
	if tcp, ok := dst.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

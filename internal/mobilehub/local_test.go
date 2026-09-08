package mobilehub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestLocalOwnerUsesTheSameHelloValidatedBoundedLineSeam(t *testing.T) {
	factory := func(input io.Reader, output io.Writer) (*mobile.Service, error) {
		return mobile.New(mobile.Config{Input: input, Output: output, HubID: "local", OwnerHostID: "local:hub", OwnerConfigGeneration: "cfg",
			Resolver: func(context.Context, string) (mobile.ResolvedTarget, error) { return mobile.ResolvedTarget{}, nil }})
	}
	stream, hello, err := StartLocal(context.Background(), factory)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if hello.Type != mobileproto.ResponseHello || hello.APIInstance == "" || hello.Capabilities == nil || !hello.Capabilities.CatalogSnapshots {
		t.Fatalf("local hello = %+v", hello)
	}
	request := mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestStatus, RequestID: "status"}
	data, _ := json.Marshal(request)
	if err := stream.WriteLine(data); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	line, err := stream.ReadLine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var response mobileproto.Response
	if err := json.NewDecoder(bytes.NewReader(line)).Decode(&response); err != nil || response.Type != mobileproto.ResponseStatus || response.RequestID != "status" {
		t.Fatalf("local response=%+v err=%v", response, err)
	}
}

func TestLocalOwnerRejectsMultilineAndOversizedRequests(t *testing.T) {
	stream := &localStream{input: &io.PipeWriter{}, cancel: func() {}, lines: make(chan []byte), done: make(chan struct{})}
	for _, line := range [][]byte{nil, []byte("one\ntwo"), []byte("one\rtwo"), make([]byte, mobileproto.MaxLineBytes+1)} {
		if err := stream.WriteLine(line); err == nil {
			t.Fatalf("invalid local owner request of %d bytes accepted", len(line))
		}
	}
}

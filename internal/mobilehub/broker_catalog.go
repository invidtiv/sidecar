package mobilehub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

// LineStream is one hello-validated owning mobile service.
type LineStream interface {
	WriteLine([]byte) error
	ReadLine(context.Context) ([]byte, error)
	Close()
}

// BoundOwner is one current owner route. Start and Validate must remain bound
// to the same runtime client and stable registration represented by Authority.
type BoundOwner struct {
	Authority CatalogAuthority
	// Capabilities is populated from this stream's validated owner hello.
	// The public broker uses the observed limits and timing; it never assumes
	// the owner's defaults match its own.
	Capabilities mobileproto.Capabilities
	Start        func(context.Context) (LineStream, mobileproto.Response, error)
	Validate     func(context.Context) error
}

// OwnerEndpoint is one public host and a binder for its current owner route.
type OwnerEndpoint struct {
	Host mobileproto.CatalogHost
	Bind func(context.Context) (BoundOwner, error)
}

// DirectorySnapshot freezes the hub configuration and available owner set for
// one catalog or target lookup. Validate rejects reload, removal or retargeting
// before the result can become authority.
type DirectorySnapshot struct {
	Identity  mobile.CatalogIdentity
	Hosts     []mobileproto.CatalogHost
	Endpoints []OwnerEndpoint
	Failures  []mobileproto.CatalogFailure
	Validate  func(context.Context) error
}

type OwnerDirectory interface {
	Snapshot(context.Context) (DirectorySnapshot, error)
}

type CatalogRouter struct {
	directory    OwnerDirectory
	ownerTimeout time.Duration
	queryTimeout time.Duration
	finalReserve time.Duration
}

const (
	OwnerCatalogTimeout  = 8 * time.Second
	CatalogQueryTimeout  = 13 * time.Second
	catalogFinalReserve  = time.Second
	composedCatalogBytes = mobileproto.MaxLineBytes - (64 << 10)
)

func NewCatalogRouter(directory OwnerDirectory) (*CatalogRouter, error) {
	if directory == nil {
		return nil, fmt.Errorf("mobile hub: owner directory is required")
	}
	return &CatalogRouter{directory: directory, ownerTimeout: OwnerCatalogTimeout, queryTimeout: CatalogQueryTimeout, finalReserve: catalogFinalReserve}, nil
}

// Query obtains one current unfiltered Project snapshot from each online
// owning service, then applies the phone's query once at the hub. Owners are
// visited sequentially, bounding live owner queues to one catalog stream.
func (r *CatalogRouter) Query(ctx context.Context, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	queryCtx, cancelQuery := context.WithTimeout(ctx, r.queryTimeout)
	defer cancelQuery()
	directory, err := r.directory.Snapshot(queryCtx)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	if err := validateDirectorySnapshot(directory); err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	sources := make([]mobile.CatalogSource, 0, len(directory.Endpoints))
	failures := append([]mobileproto.CatalogFailure(nil), directory.Failures...)
	rows, candidates, sourceBytes := 0, 0, 0
	for i, endpoint := range directory.Endpoints {
		if strings.ToLower(endpoint.Host.State) != "online" {
			continue
		}
		if err := queryCtx.Err(); err != nil {
			failures, err = appendOwnerFailure(failures, endpoint.Host, err)
			if err != nil {
				return mobileproto.CatalogSnapshot{}, err
			}
			continue
		}
		ownerCtx, cancelOwner := catalogOwnerContext(queryCtx, r.ownerTimeout, r.finalReserve)
		owner, err := endpoint.Bind(ownerCtx)
		if err != nil {
			cancelOwner()
			failures, err = appendOwnerFailure(failures, endpoint.Host, err)
			if err != nil {
				return mobileproto.CatalogSnapshot{}, err
			}
			continue
		}
		remapped, stream, err := queryBoundOwner(ownerCtx, ownerCtx, directory.Identity, &owner, fmt.Sprintf("hub-catalog-%d", i))
		cancelOwner()
		if stream != nil {
			stream.Close()
		}
		if err != nil {
			failures, err = appendOwnerFailure(failures, endpoint.Host, err)
			if err != nil {
				return mobileproto.CatalogSnapshot{}, err
			}
			continue
		}
		rowCount, candidateCount := countCatalogSource(remapped.Source)
		encoded, marshalErr := json.Marshal(remapped.Source.Snapshot)
		if marshalErr != nil {
			stream.Close()
			return mobileproto.CatalogSnapshot{}, fmt.Errorf("mobile hub: encode owner catalog bounds: %w", marshalErr)
		}
		rows += rowCount
		candidates += candidateCount
		sourceBytes += len(encoded)
		if rows > mobileproto.MaxCatalogRows || candidates > mobileproto.MaxCatalogCandidates || sourceBytes > composedCatalogBytes {
			stream.Close()
			return mobileproto.CatalogSnapshot{}, fmt.Errorf("mobile hub: combined owner catalogs exceed protocol bounds")
		}
		sources = append(sources, remapped.Source)
	}
	if err := directory.Validate(queryCtx); err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	return mobile.ComposeCatalog(query, directory.Identity, timeFromCatalogSources(sources), directory.Hosts, sources, failures)
}

// Lookup reconstructs one public selector from the current owning catalog.
// This works across a fresh hub API process; no process-local catalog map is
// treated as authority. The returned stream remains open for target resolve.
func (r *CatalogRouter) Lookup(ctx context.Context, selector string, expected mobileproto.TargetIdentity) (BoundOwner, LineStream, TargetBinding, error) {
	if strings.TrimSpace(selector) == "" || len(selector) > mobileproto.MaxTargetBytes || expected.HubID == "" || expected.OwnerHostID == "" {
		return BoundOwner{}, nil, TargetBinding{}, fmt.Errorf("mobile hub: incomplete public target selection")
	}
	lookupCtx, cancelLookup := context.WithTimeout(ctx, r.queryTimeout)
	defer cancelLookup()
	directory, err := r.directory.Snapshot(lookupCtx)
	if err != nil {
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	if err := validateDirectorySnapshot(directory); err != nil {
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	if expected.HubID != directory.Identity.HubID {
		return BoundOwner{}, nil, TargetBinding{}, fmt.Errorf("mobile hub: public target belongs to another hub")
	}
	var endpoint *OwnerEndpoint
	for i := range directory.Endpoints {
		if directory.Endpoints[i].Host.ID == expected.OwnerHostID {
			endpoint = &directory.Endpoints[i]
			break
		}
	}
	if endpoint == nil || strings.ToLower(endpoint.Host.State) != "online" {
		return BoundOwner{}, nil, TargetBinding{}, fmt.Errorf("mobile hub: owning host is unavailable")
	}
	ownerCtx, cancelOwner := catalogOwnerContext(lookupCtx, r.ownerTimeout, r.finalReserve)
	defer cancelOwner()
	owner, err := endpoint.Bind(ownerCtx)
	if err != nil {
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	remapped, stream, err := queryBoundOwner(ownerCtx, ctx, directory.Identity, &owner, "hub-target-lookup")
	if err != nil {
		if stream != nil {
			stream.Close()
		}
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	if _, err := mobile.ComposeCatalog(mobileproto.CatalogQuery{Sort: "project"}, directory.Identity, timeFromCatalogSources([]mobile.CatalogSource{remapped.Source}),
		directory.Hosts, []mobile.CatalogSource{remapped.Source}, nil); err != nil {
		stream.Close()
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	binding, ok := remapped.Bindings[selector]
	if !ok || binding.PublicExpected != expected {
		stream.Close()
		return BoundOwner{}, nil, TargetBinding{}, fmt.Errorf("mobile hub: public target identity changed")
	}
	if err := owner.Validate(lookupCtx); err != nil {
		stream.Close()
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	if err := directory.Validate(lookupCtx); err != nil {
		stream.Close()
		return BoundOwner{}, nil, TargetBinding{}, err
	}
	ownerValidate := owner.Validate
	directoryValidate := directory.Validate
	owner.Validate = func(validateCtx context.Context) error {
		if err := ownerValidate(validateCtx); err != nil {
			return err
		}
		return directoryValidate(validateCtx)
	}
	return owner, stream, binding, nil
}

func queryBoundOwner(operationCtx, streamCtx context.Context, hubIdentity mobile.CatalogIdentity, owner *BoundOwner, requestID string) (RemappedCatalog, LineStream, error) {
	if owner == nil || owner.Start == nil || owner.Validate == nil || owner.Authority.OwnerHostID == "" {
		return RemappedCatalog{}, nil, fmt.Errorf("mobile hub: incomplete bound owner")
	}
	if err := owner.Validate(operationCtx); err != nil {
		return RemappedCatalog{}, nil, err
	}
	startCtx, cancelStart := context.WithCancel(streamCtx)
	stopStartupDeadline := context.AfterFunc(operationCtx, cancelStart)
	stream, hello, err := owner.Start(startCtx)
	if err != nil {
		stopStartupDeadline()
		cancelStart()
		return RemappedCatalog{}, nil, err
	}
	stream = &retainedLineStream{LineStream: stream, cancel: cancelStart}
	stopStreamDeadline := context.AfterFunc(operationCtx, stream.Close)
	stopOperationDeadline := func() bool {
		startupStopped := stopStartupDeadline()
		streamStopped := stopStreamDeadline()
		return startupStopped && streamStopped
	}
	fail := func(err error) (RemappedCatalog, LineStream, error) {
		stopOperationDeadline()
		stream.Close()
		return RemappedCatalog{}, nil, err
	}
	if hello.Capabilities == nil {
		return fail(fmt.Errorf("mobile hub: owner hello omitted negotiated capabilities"))
	}
	owner.Capabilities = *hello.Capabilities
	request := mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestSessions, RequestID: requestID,
		CatalogQuery: &mobileproto.CatalogQuery{Sort: "project"}}
	data, err := json.Marshal(request)
	if err != nil {
		return fail(err)
	}
	if err := stream.WriteLine(data); err != nil {
		return fail(err)
	}
	line, err := stream.ReadLine(operationCtx)
	if err != nil {
		return fail(err)
	}
	var response mobileproto.Response
	if err := json.Unmarshal(line, &response); err != nil || response.Version != mobileproto.Version || response.Type != mobileproto.ResponseSessions ||
		response.RequestID != requestID || response.Catalog == nil {
		return fail(fmt.Errorf("mobile hub: owner returned an invalid catalog response"))
	}
	if err := owner.Validate(operationCtx); err != nil {
		return fail(err)
	}
	authority := owner.Authority
	authority.HubID = hubIdentity.HubID
	authority.HubConfigGeneration = hubIdentity.OwnerConfigGeneration
	remapped, err := RemapOwnerCatalog(authority, *response.Catalog)
	if err != nil {
		return fail(err)
	}
	if !stopOperationDeadline() || operationCtx.Err() != nil {
		stream.Close()
		return RemappedCatalog{}, nil, operationCtx.Err()
	}
	return remapped, stream, nil
}

type retainedLineStream struct {
	LineStream
	cancel context.CancelFunc
}

func (s *retainedLineStream) Close() {
	s.LineStream.Close()
	s.cancel()
}

func catalogOwnerContext(queryCtx context.Context, ownerTimeout, finalReserve time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(ownerTimeout)
	if queryDeadline, ok := queryCtx.Deadline(); ok {
		reservedDeadline := queryDeadline.Add(-finalReserve)
		if reservedDeadline.Before(deadline) {
			deadline = reservedDeadline
		}
	}
	return context.WithDeadline(queryCtx, deadline)
}

func validateDirectorySnapshot(directory DirectorySnapshot) error {
	if directory.Identity.HubID == "" || directory.Identity.OwnerHostID == "" || directory.Identity.OwnerConfigGeneration == "" || directory.Validate == nil ||
		len(directory.Hosts) > mobileproto.MaxCatalogHosts || len(directory.Endpoints) > mobileproto.MaxCatalogHosts || len(directory.Failures) > mobileproto.MaxCatalogFailures {
		return fmt.Errorf("mobile hub: incomplete or oversized owner directory")
	}
	seenHosts := make(map[string]mobileproto.CatalogHost, len(directory.Hosts))
	for _, host := range directory.Hosts {
		if host.ID == "" || host.State == "" {
			return fmt.Errorf("mobile hub: invalid or duplicate directory host")
		}
		if _, exists := seenHosts[host.ID]; exists {
			return fmt.Errorf("mobile hub: invalid or duplicate directory host")
		}
		seenHosts[host.ID] = host
	}
	seenEndpoints := make(map[string]bool, len(directory.Endpoints))
	for _, endpoint := range directory.Endpoints {
		directoryHost, exists := seenHosts[endpoint.Host.ID]
		if endpoint.Host.ID == "" || !exists || !strings.EqualFold(endpoint.Host.State, directoryHost.State) ||
			endpoint.Host.Local != directoryHost.Local || seenEndpoints[endpoint.Host.ID] || endpoint.Bind == nil {
			return fmt.Errorf("mobile hub: invalid or duplicate owner endpoint")
		}
		seenEndpoints[endpoint.Host.ID] = true
	}
	return nil
}

func appendOwnerFailure(failures []mobileproto.CatalogFailure, host mobileproto.CatalogHost, failure error) ([]mobileproto.CatalogFailure, error) {
	if len(failures) >= mobileproto.MaxCatalogFailures {
		return nil, fmt.Errorf("mobile hub: owner failures exceed protocol limit")
	}
	return append(failures, mobileproto.CatalogFailure{Scope: "host", ID: host.ID, Name: host.Name, State: "unavailable", Detail: failure.Error()}), nil
}

func countCatalogSource(source mobile.CatalogSource) (rows, candidates int) {
	for _, section := range source.Snapshot.Sections {
		rows += len(section.Rows)
		for _, row := range section.Rows {
			candidates += len(row.Candidates)
		}
	}
	return rows, candidates
}

func timeFromCatalogSources(sources []mobile.CatalogSource) time.Time {
	var latest time.Time
	for _, source := range sources {
		observed, err := time.Parse(time.RFC3339Nano, source.Snapshot.ObservedAt)
		if err == nil && observed.After(latest) {
			latest = observed
		}
	}
	return latest
}

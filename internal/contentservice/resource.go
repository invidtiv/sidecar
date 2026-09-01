package contentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceprovider"
	"github.com/marcus/sidecar/internal/terminallink"
)

// ProviderDescriptor is one host provider's validated contribution to a
// describe result, in configured order. The fingerprint hashes this wire
// content; process-local snapshot generation is never part of it.
type ProviderDescriptor struct {
	Instance   string               `json:"instance"`
	Order      int                  `json:"order"`
	Kind       string               `json:"kind,omitempty"`
	Name       string               `json:"name,omitempty"`
	Version    string               `json:"version,omitempty"`
	Matchers   []ResourceMatcherDTO `json:"matchers"`
	ClaimHosts []string             `json:"claimHosts,omitempty"`
}

// ResourceMatcherDTO is one validated matcher on the describe wire.
type ResourceMatcherDTO struct {
	ID       string `json:"id"`
	Pattern  string `json:"pattern"`
	Priority int    `json:"priority,omitempty"`
}

// FingerprintDescriptors returns a deterministic v1:sha256 of the validated
// ordered descriptor wire content. Empty and nil slices fingerprint the same
// (`{"descriptors":[]}`). Snapshot.Generation() is not an input.
func FingerprintDescriptors(descriptors []ProviderDescriptor) string {
	payload := struct {
		Descriptors []ProviderDescriptor `json:"descriptors"`
	}{Descriptors: descriptors}
	if payload.Descriptors == nil {
		payload.Descriptors = []ProviderDescriptor{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "v1:err"
	}
	sum := sha256.Sum256(raw)
	return "v1:" + hex.EncodeToString(sum[:])
}

// TerminalMatchersFrom compiles validated descriptors into scanner matchers
// in the same precedence SnapshotStore uses: configured order, then
// descending priority, then matcher ID.
func TerminalMatchersFrom(descriptors []ProviderDescriptor) ([]terminallink.ResourceMatcher, error) {
	sets := make([]resourceprovider.DescribedSet, 0, len(descriptors))
	for _, d := range descriptors {
		matchers := make([]resourceprovider.Matcher, 0, len(d.Matchers))
		for _, m := range d.Matchers {
			matchers = append(matchers, resourceprovider.Matcher{ID: m.ID, Pattern: m.Pattern, Priority: m.Priority})
		}
		sets = append(sets, resourceprovider.DescribedSet{
			Instance:   d.Instance,
			Order:      d.Order,
			Matchers:   matchers,
			ClaimHosts: d.ClaimHosts,
		})
	}
	store := resourceprovider.NewSnapshotStore()
	if err := store.Replace(sets); err != nil {
		return nil, err
	}
	return store.Current().TerminalMatchers(), nil
}

// Describe runs the host provider describe pipeline and returns validated
// ordered descriptors plus a deterministic fingerprint. When ifRevision
// matches, descriptors are omitted.
func Describe(ctx context.Context, ifRevision string) (DescribeResult, error) {
	return Default().Describe(ctx, ifRevision)
}

// Describe is Service.Describe.
func (s *Service) Describe(ctx context.Context, ifRevision string) (DescribeResult, error) {
	if err := ctx.Err(); err != nil {
		return DescribeResult{}, err
	}
	mgr, err := s.resourceManager()
	if err != nil {
		return DescribeResult{}, err
	}
	_ = mgr.DescribeAll(ctx)
	if err := ctx.Err(); err != nil {
		return DescribeResult{}, err
	}
	descriptors := descriptorsFromManager(mgr)
	fp := FingerprintDescriptors(descriptors)
	if ifRevision != "" && ifRevision == fp {
		return DescribeResult{Fingerprint: fp, NotModified: true}, nil
	}
	return DescribeResult{Fingerprint: fp, Descriptors: descriptors}, nil
}

// ResolveResourceRef validates a resource reference against a workspace
// identity without invoking the provider.
func (s *Service) ResolveResourceRef(ctx context.Context, workspaceID string, ref resource.Reference) (ResolveResult, error) {
	if err := ctx.Err(); err != nil {
		return ResolveResult{}, err
	}
	if !ref.Valid() {
		return ResolveResult{}, Rejected("resource reference is empty or exceeds its bounds")
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{
		Kind:      KindResource,
		Workspace: ws.ID,
		Target:    ref.Locator,
		Display:   ref.Locator,
		Provider:  ref.Instance,
		Matcher:   ref.Matcher,
	}, nil
}

// ReadResource invokes the host manager and returns a wire document (or a
// typed resource error envelope). Credentials, commands, and unsanitized
// bodies never enter the result.
func (s *Service) ReadResource(ctx context.Context, workspaceID string, ref resource.Reference, refresh bool) (ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadResult{}, err
	}
	if !ref.Valid() {
		return ReadResult{}, Rejected("resource reference is empty or exceeds its bounds")
	}
	ws, err := s.lookupWorkspace(ctx, workspaceID)
	if err != nil {
		return ReadResult{}, err
	}
	mgr, err := s.resourceManager()
	if err != nil {
		return ReadResult{}, err
	}
	doc, resolveErr := mgr.Resolve(ctx, ref, refresh)
	if resolveErr != nil {
		typed := resourceprovider.AsResourceError(resolveErr)
		return resourceErrorResult(ws.ID, ref, typed), nil
	}
	wire := wireDocumentFrom(doc)
	return ReadResult{
		Kind:      KindResource,
		Operation: OpResource,
		Workspace: ws.ID,
		Target:    ref.Locator,
		Display:   doc.Title,
		Provider:  ref.Instance,
		Matcher:   ref.Matcher,
		Revision:  revisionForValue(wire),
		Resource:  wire,
	}, nil
}

func resourceErrorResult(workspace string, ref resource.Reference, err *resource.Error) ReadResult {
	if err == nil {
		err = resource.Errorf(resource.CodeInternal, "provider failed")
	}
	retryable := err.Retryable
	wire := &resource.WireError{
		Code:      string(err.Code),
		Message:   err.Message,
		Retryable: &retryable,
		SetupHint: err.SetupHint,
	}
	return ReadResult{
		Kind:          KindResource,
		Operation:     OpResource,
		Workspace:     workspace,
		Target:        ref.Locator,
		Provider:      ref.Instance,
		Matcher:       ref.Matcher,
		Revision:      revisionForValue(wire),
		ResourceError: wire,
	}
}

func wireDocumentFrom(doc resource.Document) *resource.WireDocument {
	w := &resource.WireDocument{
		Identity:        doc.Identity,
		Title:           doc.Title,
		Subtitle:        doc.Subtitle,
		SourceURL:       doc.SourceURL,
		FreshForSeconds: doc.FreshFor.Seconds(),
	}
	if !doc.UpdatedAt.IsZero() {
		w.UpdatedAt = doc.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if doc.Status != nil {
		w.Status = &resource.WireStatus{Label: doc.Status.Label, Tone: string(doc.Status.Tone)}
	}
	if len(doc.Fields) > 0 {
		w.Fields = make([]resource.WireField, 0, len(doc.Fields))
		for _, f := range doc.Fields {
			w.Fields = append(w.Fields, resource.WireField{Label: f.Label, Value: f.Value, Kind: string(f.Kind)})
		}
	}
	if doc.Body != nil {
		w.Body = &resource.WireBody{Format: string(doc.Body.Format), Text: doc.Body.Text}
	}
	return w
}

func descriptorsFromManager(mgr *resourceprovider.Manager) []ProviderDescriptor {
	if mgr == nil {
		return []ProviderDescriptor{}
	}
	snap := mgr.Snapshot()
	statuses := mgr.Statuses()
	info := make(map[string]resourceprovider.Status, len(statuses))
	for _, st := range statuses {
		info[st.Instance] = st
	}
	grouped := map[string]*ProviderDescriptor{}
	var order []string
	for _, m := range snap.Matchers() {
		d, ok := grouped[m.Instance]
		if !ok {
			d = &ProviderDescriptor{
				Instance:   m.Instance,
				Order:      m.Order,
				Matchers:   []ResourceMatcherDTO{},
				ClaimHosts: snap.ClaimHosts(m.Instance),
			}
			if st, found := info[m.Instance]; found {
				d.Kind = st.Info.Kind
				d.Name = st.Info.Name
				d.Version = st.Info.Version
			}
			grouped[m.Instance] = d
			order = append(order, m.Instance)
		}
		d.Matchers = append(d.Matchers, ResourceMatcherDTO{ID: m.ID, Pattern: m.Pattern, Priority: m.Priority})
	}
	sort.SliceStable(order, func(i, j int) bool {
		return grouped[order[i]].Order < grouped[order[j]].Order
	})
	out := make([]ProviderDescriptor, 0, len(order))
	for _, id := range order {
		out = append(out, *grouped[id])
	}
	if out == nil {
		return []ProviderDescriptor{}
	}
	return out
}

func (s *Service) resourceManager() (*resourceprovider.Manager, error) {
	if s != nil && s.NewResourceManager != nil {
		return s.NewResourceManager()
	}
	load := defaultLoadConfig
	if s != nil && s.LoadConfig != nil {
		load = s.LoadConfig
	}
	cfg, err := load()
	if err != nil {
		return nil, Internal("load config", err)
	}
	if cfg == nil {
		cfg = config.Default()
	}
	providers, disabled, err := resourceprovider.FromConfig(cfg.TerminalResources, resourceprovider.Options{
		Dir: resourceProviderDir(),
	})
	if err != nil {
		return nil, Internal("resource provider config", err)
	}
	mgr := resourceprovider.NewManager(resourceprovider.ManagerOptions{})
	mgr.SetProviders(providers, disabled)
	return mgr, nil
}

func resourceProviderDir() string {
	path := config.ConfigPath()
	if path == "" {
		return os.TempDir()
	}
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return os.TempDir()
	}
	return dir
}

// EncodeDescribeResult writes the compact JSON object for describe.
func EncodeDescribeResult(result DescribeResult) ([]byte, error) {
	raw, err := marshalJSON(result)
	if err != nil {
		return nil, Internal("encode describe result", err)
	}
	if len(raw) > MaxEncodedBytes {
		return nil, Rejected("encoded describe result exceeds %d bytes", MaxEncodedBytes)
	}
	return raw, nil
}

// SanitizeWireDocument is the viewer-side gate: a hostile host body is
// refused rather than adopted. Credentials never appear on WireDocument.
func SanitizeWireDocument(w *resource.WireDocument) (resource.Document, error) {
	doc, structural := resource.SanitizeDocument(w)
	if structural != nil {
		return resource.Document{}, Rejected("resource document refused: %s", structural.Detail)
	}
	return doc, nil
}

// ResourceErrorFromWire turns a host typed-error envelope into a card error.
func ResourceErrorFromWire(w *resource.WireError) *resource.Error {
	return resource.SanitizeError(w)
}

// DocumentFreshUntil is when a sanitized document's viewer cache entry expires.
func DocumentFreshUntil(doc resource.Document, now time.Time) time.Time {
	fresh := doc.FreshFor
	if fresh <= 0 {
		fresh = resource.DefaultFreshFor
	}
	return now.Add(fresh)
}

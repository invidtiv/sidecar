package uiapi

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Access requests let a browser with no credential ask to be let in, and an
// already-trusted surface approve it by typing the short code the browser
// shows. Requests are process-local like pairing codes: a restart drops them
// and the browser asks again. Approval creates a registration for exactly the
// requesting browser's public key and origin; nothing transferable is handed
// to anyone. The rules below are state-free so every surface (HTTP, CLI, TUI)
// applies the same ones.

const (
	accessRequestTTL = 5 * time.Minute
	// accessSettledRetention keeps a settled request answerable to its poller
	// after approval, denial or expiry, so the browser learns the outcome.
	accessSettledRetention     = 5 * time.Minute
	maxPendingAccessRequests   = 16
	maxPendingAccessPerAddress = 2
	maxSettledAccessRequests   = 64
	maxAccessCodeFailures      = 5
	accessCodeFailureWindow    = time.Minute
	maxDeviceLabelRunes        = 64
	accessReplaceInterval      = time.Second
	accessCodeLength           = 6
	accessCodeAlphabet         = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32
	approvedViaLink            = "link"
	approvedViaCLI             = "cli"
	approvedViaTUI             = "tui"
	approvedViaBrowserPrefix   = "browser:"
	approvedViaTailnetPrefix   = "tailnet:"
	accessStatusPending        = "pending"
	accessStatusApproved       = "approved"
	accessStatusDenied         = "denied"
	accessStatusExpired        = "expired"
	accessPollScheme           = "Request"
)

var (
	errAccessCodeInvalid     = errors.New("no pending access request has that code")
	errAccessRequestNotFound = errors.New("no pending access request has that id")
	errAccessAttempts        = errors.New("too many wrong access codes")
)

// accessRequest is one browser's request for access.
type accessRequest struct {
	id           string
	pollHash     string // hashToken(poll secret); the secret is never kept
	code         string // normalized, without the display hyphen
	publicKey    BrowserPublicKey
	registration string
	origin       string
	address      string
	label        string
	created      time.Time
	expires      time.Time
	status       string
	settled      time.Time
	approvedBy   string // approved_via of the resulting registration
}

func (r *accessRequest) info() AccessRequestInfo {
	return AccessRequestInfo{RequestID: r.id, Label: r.label, Origin: r.origin, Address: r.address, CreatedAt: r.created.UTC(), ExpiresAt: r.expires.UTC()}
}

// MultipleWaiting is the plain warning shown when more than one browser is
// waiting: every request can look identical, so the code on the person's own
// screen is the only thing that tells them apart.
func MultipleWaiting(n int) string {
	if n < 2 {
		return ""
	}
	return fmt.Sprintf("%d browsers are waiting; make sure the code is the one on your screen.", n)
}

// NormalizeAccessCode turns what a person typed into the canonical code:
// case-insensitive, hyphens and spaces ignored, and Crockford's look-alikes
// (O for 0, I and L for 1) accepted. It reports false for anything that
// cannot be a code.
func NormalizeAccessCode(input string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(input) {
		switch r {
		case '-', ' ', '\t':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if !strings.ContainsRune(accessCodeAlphabet, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	code := b.String()
	return code, len(code) == accessCodeLength
}

// FormatAccessCode is the display form, K7Q-4MX.
func FormatAccessCode(code string) string {
	if len(code) != accessCodeLength {
		return code
	}
	return code[:3] + "-" + code[3:]
}

func newAccessCode() (string, error) {
	raw := make([]byte, accessCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, accessCodeLength)
	for i, b := range raw {
		out[i] = accessCodeAlphabet[int(b)%len(accessCodeAlphabet)] // 256 is a multiple of 32: uniform
	}
	return string(out), nil
}

// cleanDeviceLabel makes a client-suggested device name safe to show: control
// and format characters (including bidirectional overrides) are dropped,
// whitespace runs collapse to one space, and the result is capped. It is a
// claim the browser makes about itself, never proof.
func cleanDeviceLabel(label string) string {
	var b strings.Builder
	space := false
	for _, r := range label {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			if unicode.IsSpace(r) {
				space = true
			}
			continue
		}
		if unicode.IsSpace(r) || blankFiller(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > maxDeviceLabelRunes {
		out = string([]rune(out)[:maxDeviceLabelRunes])
		out = strings.TrimRight(out, " ")
	}
	if !hasVisibleRune(out) {
		// Only combining marks or fillers: it would show as nothing, which is
		// no name at all.
		return ""
	}
	return out
}

// blankFiller reports letters that render as blank space (Hangul and
// halfwidth fillers, the blank Braille pattern) though Unicode does not call
// them spaces.
func blankFiller(r rune) bool {
	switch r {
	case '\u115F', '\u1160', '\u3164', '\uFFA0', '\u2800':
		return true
	}
	return false
}

func hasVisibleRune(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.In(r, unicode.Mn, unicode.Me) {
			return true
		}
	}
	return false
}

// mayApprove is who may list, approve and deny access requests and manage
// devices: the trusted Local socket (CLI and TUI), a signed-in browser, and
// an allowed tailnet login. A paired origin is an embedding app with its own
// token; it never lets another browser in.
func mayApprove(c caller) bool {
	switch c.auth {
	case "local", "session", "tailnet":
		return true
	case "ticket":
		// A browser opens its streams with a ticket; the ticket carries the
		// client that bought it. A browser session or tailnet login is an
		// approver, a paired origin ("origin:") is not.
		return strings.HasPrefix(c.client, "session:") || strings.HasPrefix(c.client, "tailnet:")
	}
	return false
}

// approvedVia names the surface that approved a request, as recorded on the
// resulting registration. Only the Local socket may say which local surface
// it is; anything else is identified by its own credential.
func approvedVia(c caller, surface string) (string, error) {
	switch c.auth {
	case "local":
		switch surface {
		case "", approvedViaCLI:
			return approvedViaCLI, nil
		case approvedViaTUI:
			return approvedViaTUI, nil
		}
		return "", errors.New("surface must be cli or tui")
	case "session":
		if surface != "" {
			return "", errors.New("surface is accepted only on the local socket")
		}
		return approvedViaBrowserPrefix + strings.TrimPrefix(c.client, "session:"), nil
	case "tailnet":
		if surface != "" {
			return "", errors.New("surface is accepted only on the local socket")
		}
		return approvedViaTailnetPrefix + c.login, nil
	}
	return "", errors.New("this caller may not approve access")
}

func validApprovedVia(via string) bool {
	switch {
	case via == approvedViaLink, via == approvedViaCLI, via == approvedViaTUI:
		return true
	case strings.HasPrefix(via, approvedViaBrowserPrefix):
		return len(via) > len(approvedViaBrowserPrefix)
	case strings.HasPrefix(via, approvedViaTailnetPrefix):
		return len(via) > len(approvedViaTailnetPrefix)
	}
	return false
}

// accessEvictions applies the outstanding-request limits to a new request
// from address. A new request is never refused for capacity: when its
// address already holds the per-address limit, or the server holds the total
// limit, the oldest pending request in that bucket is evicted instead (its
// browser's poll reports expired and asks again). Someone nearby can race a
// request in, but cannot shut everyone else out. Behind a proxy every request
// has the proxy's address and shares one bucket. pending is oldest first; a
// pending request for the same registration is replaced, so it is not
// counted here.
func accessEvictions(pending []*accessRequest, address, registration string) []*accessRequest {
	var others, sameAddress []*accessRequest
	for _, r := range pending {
		if r.registration == registration {
			continue
		}
		others = append(others, r)
		if r.address == address {
			sameAddress = append(sameAddress, r)
		}
	}
	var evicted []*accessRequest
	gone := map[*accessRequest]bool{}
	for len(sameAddress) >= maxPendingAccessPerAddress {
		evicted = append(evicted, sameAddress[0])
		gone[sameAddress[0]] = true
		sameAddress = sameAddress[1:]
	}
	remaining := len(others) - len(evicted)
	for _, r := range others {
		if remaining < maxPendingAccessRequests {
			break
		}
		if !gone[r] {
			evicted = append(evicted, r)
			gone[r] = true
			remaining--
		}
	}
	return evicted
}

// replacementTooSoon reports whether the same browser asked again too soon
// after its pending request. Replacement skips the capacity limits, so it is
// rate-limited instead.
func replacementTooSoon(pending []*accessRequest, registration string, now time.Time) bool {
	for _, r := range pending {
		if r.registration == registration && now.Sub(r.created) < accessReplaceInterval {
			return true
		}
	}
	return false
}

// recentFailures keeps the wrong-code failures inside the window.
func recentFailures(failures []time.Time, now time.Time) []time.Time {
	kept := failures[:0]
	for _, at := range failures {
		if now.Sub(at) < accessCodeFailureWindow {
			kept = append(kept, at)
		}
	}
	return kept
}

// pruneAccessLocked expires pending requests past their deadline and forgets
// settled ones past retention. It reports whether any pending request
// expired. a.mu is held.
func (a *authStore) pruneAccessLocked(now time.Time) bool {
	expired := false
	settled := make([]*accessRequest, 0)
	for id, r := range a.access {
		if r.status == accessStatusPending && !now.Before(r.expires) {
			r.status, r.settled = accessStatusExpired, r.expires
			expired = true
		}
		if r.status != accessStatusPending {
			if now.Sub(r.settled) >= accessSettledRetention {
				delete(a.access, id)
				continue
			}
			settled = append(settled, r)
		}
	}
	if len(settled) > maxSettledAccessRequests {
		sort.Slice(settled, func(i, j int) bool { return settled[i].settled.Before(settled[j].settled) })
		for _, r := range settled[:len(settled)-maxSettledAccessRequests] {
			delete(a.access, r.id)
		}
	}
	for client, failures := range a.accessFailures {
		if kept := recentFailures(failures, now); len(kept) == 0 {
			delete(a.accessFailures, client)
		} else {
			a.accessFailures[client] = kept
		}
	}
	return expired
}

func (a *authStore) pendingAccessLocked() []*accessRequest {
	out := make([]*accessRequest, 0, len(a.access))
	for _, r := range a.access {
		if r.status == accessStatusPending {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].created.Equal(out[j].created) {
			return out[i].created.Before(out[j].created)
		}
		return out[i].id < out[j].id
	})
	return out
}

// createAccessRequest records a request from a browser that holds key at
// origin. The poll secret is returned once and kept only as a hash.
func (a *authStore) createAccessRequest(origin, address, label string, key BrowserPublicKey) (AccessRequestCreated, AccessRequestInfo, error) {
	if _, err := key.ecdsaKey(); err != nil {
		return AccessRequestCreated{}, AccessRequestInfo{}, err
	}
	key.KeyOps, key.Ext = nil, nil
	registration := browserRegistrationID(origin, key)
	id, err := randomToken(12)
	if err != nil {
		return AccessRequestCreated{}, AccessRequestInfo{}, err
	}
	secret, err := randomToken(32)
	if err != nil {
		return AccessRequestCreated{}, AccessRequestInfo{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneAccessLocked(now)
	pending := a.pendingAccessLocked()
	if replacementTooSoon(pending, registration, now) {
		return AccessRequestCreated{}, AccessRequestInfo{}, errTooManyOutstanding
	}
	for _, r := range accessEvictions(pending, address, registration) {
		r.status, r.settled = accessStatusExpired, now
	}
	used := map[string]bool{}
	for _, r := range pending {
		if r.status != accessStatusPending {
			continue
		}
		if r.registration == registration {
			// The same browser asked again (a reload); its earlier request
			// is superseded rather than left to count against the limits.
			r.status, r.settled = accessStatusExpired, now
			continue
		}
		used[r.code] = true
	}
	var code string
	for attempt := 0; ; attempt++ {
		if code, err = newAccessCode(); err != nil {
			return AccessRequestCreated{}, AccessRequestInfo{}, err
		}
		if !used[code] {
			break
		}
		if attempt > 32 {
			return AccessRequestCreated{}, AccessRequestInfo{}, errors.New("could not pick an unused access code")
		}
	}
	r := &accessRequest{id: id, pollHash: hashToken(secret), code: code, publicKey: key, registration: registration,
		origin: origin, address: address, label: cleanDeviceLabel(label), created: now, expires: now.Add(accessRequestTTL), status: accessStatusPending}
	a.access[id] = r
	return AccessRequestCreated{RequestID: id, PollSecret: secret, Code: FormatAccessCode(code), ExpiresAt: r.expires.UTC()}, r.info(), nil
}

// pollAccessRequest answers the requesting browser. An unknown id and a wrong
// secret are indistinguishable.
func (a *authStore) pollAccessRequest(id, secret, origin string) (AccessRequestStatus, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneAccessLocked(a.now())
	r, ok := a.access[id]
	if !ok || secret == "" || subtle.ConstantTimeCompare([]byte(r.pollHash), []byte(hashToken(secret))) != 1 || (origin != "" && origin != r.origin) {
		return AccessRequestStatus{}, false
	}
	out := AccessRequestStatus{Status: r.status, ExpiresAt: r.expires.UTC()}
	if r.status == accessStatusApproved {
		out.RegistrationID = r.registration
	}
	return out, true
}

// listAccessRequests returns pending requests, oldest first, without codes.
func (a *authStore) listAccessRequests() []AccessRequestInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneAccessLocked(a.now())
	out := []AccessRequestInfo{}
	for _, r := range a.pendingAccessLocked() {
		out = append(out, r.info())
	}
	return out
}

// approveAccess approves the pending request whose code matches, creating
// the registration for that request's public key and origin. Wrong codes
// count against approver; once it has five in a minute every attempt,
// right or wrong, is refused until the window passes.
func (a *authStore) approveAccess(approver, code, via string) (AccessApproval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneAccessLocked(now)
	if len(recentFailures(a.accessFailures[approver], now)) >= maxAccessCodeFailures {
		return AccessApproval{}, errAccessAttempts
	}
	var match *accessRequest
	for _, r := range a.pendingAccessLocked() {
		if subtle.ConstantTimeCompare([]byte(r.code), []byte(code)) == 1 {
			match = r
		}
	}
	if match == nil {
		a.accessFailures[approver] = append(a.accessFailures[approver], now)
		return AccessApproval{}, errAccessCodeInvalid
	}
	id, err := a.upsertRegistrationLocked(match.origin, match.publicKey, registrationApproval{label: match.label, via: via})
	if err != nil {
		return AccessApproval{}, err
	}
	match.status, match.settled, match.approvedBy = accessStatusApproved, now, via
	// Report the registration as stored, so the device list agrees.
	stored := a.sessions[id]
	return AccessApproval{RequestID: match.id, RegistrationID: id, Label: stored.Label, Origin: match.origin, Address: match.address, ApprovedVia: stored.ApprovedVia, ApprovedAt: stored.ApprovedAt.UTC()}, nil
}

// denyAccess refuses one pending request by id.
func (a *authStore) denyAccess(id string) (AccessDenial, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneAccessLocked(now)
	r, ok := a.access[id]
	if !ok || r.status != accessStatusPending {
		return AccessDenial{}, errAccessRequestNotFound
	}
	r.status, r.settled = accessStatusDenied, now
	return AccessDenial{RequestID: id, Status: accessStatusDenied}, nil
}

// sweepAccess expires overdue requests and reports how many remain pending.
func (a *authStore) sweepAccess() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneAccessLocked(a.now())
	return len(a.pendingAccessLocked())
}

// nextAccessExpiry is when the earliest pending request expires.
func (a *authStore) nextAccessExpiry() (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var next time.Time
	for _, r := range a.pendingAccessLocked() {
		if next.IsZero() || r.expires.Before(next) {
			next = r.expires
		}
	}
	return next, !next.IsZero()
}

func (a *authStore) pendingAccessCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pendingAccessLocked())
}

// listDevices returns live browser registrations, most recently used first.
func (a *authStore) listDevices(current string) ([]Device, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.reloadSessionsLocked(); err != nil {
		return nil, err
	}
	now := a.now()
	out := []Device{}
	for id, s := range a.sessions {
		if !now.Before(s.ExpiresAt) {
			continue
		}
		out = append(out, Device{ID: id, Origin: s.Origin, Label: s.Label, ApprovedVia: s.ApprovedVia, ApprovedAt: s.ApprovedAt.UTC(),
			CreatedAt: s.CreatedAt.UTC(), LastUsedAt: s.LastUsedAt.UTC(), ExpiresAt: s.ExpiresAt.UTC(), Current: current != "" && sessionClient(id) == current})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastUsedAt.Equal(out[j].LastUsedAt) {
			return out[i].LastUsedAt.After(out[j].LastUsedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// revokeDevice removes one registration with its bearers, challenges and
// unused tickets. It reports whether the registration existed.
func (a *authStore) revokeDevice(id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	found := false
	if err := a.withSessionsLocked(func(records map[string]session) bool {
		_, found = records[id]
		delete(records, id)
		return found
	}); err != nil {
		return false, err
	}
	for hash, bearer := range a.bearers {
		if bearer.registration == id {
			delete(a.bearers, hash)
		}
	}
	for hash, proof := range a.proofs {
		if proof.registration == id {
			delete(a.proofs, hash)
		}
	}
	client := sessionClient(id)
	for key, g := range a.tickets {
		if g.client == client {
			delete(a.tickets, key)
		}
	}
	return found, nil
}

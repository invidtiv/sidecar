package uiapi

// addAccessSpec documents the access-request and device routes. The wire
// contract and the full status-code list are in docs/reference/ui-api.md.
func addAccessSpec(paths map[string]any, add func(string, string, string, string, []string, bool), all []string) {
	browser := []string{"browser"}
	op := func(path, method string) map[string]any {
		return paths[path].(map[string]any)[method].(map[string]any)
	}
	idParam := map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,128}$"}}
	approvers := "Approvers only: the Local socket, browser sessions and allowed tailnet logins. A paired origin gets 403 approver_refused."
	recheck := " Admission is re-checked after the body arrives: a credential revoked mid-request gets 401 unauthenticated and changes nothing."

	add(accessRequestsPath, "post", "AccessRequestCreate", "AccessRequestCreated", browser, true)
	create := op(accessRequestsPath, "post")
	create["description"] = "A browser with no credential asks for access. No bearer; requires the Browser listener's own exact Origin and the mutation headers, like the pairing exchange. The server records the origin and the TCP peer address itself. Never refused for capacity: past 16 pending in total or 2 per address, the oldest pending request in that bucket is evicted and its poll reports expired. A request from the same public key and origin replaces that browser's earlier one, at most once a second (429 too_many_outstanding). Expires after 5 minutes. Returns the poll secret once; never returns a credential."
	add(accessRequestsPath, "get", "", "AccessRequestList", all, false)
	list := op(accessRequestsPath, "get")
	list["description"] = "Pending access requests, oldest first, without their codes. " + approvers
	list["x-callers"] = "approvers"

	add(accessRequestPath, "get", "", "AccessRequestStatus", browser, true)
	poll := op(accessRequestPath, "get")
	poll["description"] = "The requesting browser polls its request with Authorization: Request <poll_secret>. An unknown id, a wrong secret and a forgotten request are all 404 access_request_not_found; settled requests stay answerable for 5 minutes. On approved, renew a session with the browser key (session-proof) for registration_id."
	poll["security"] = []any{map[string]any{"accessRequestPoll": []string{}}}
	poll["parameters"] = []any{idParam}

	add(accessApprovePath, "post", "AccessApproveRequest", "AccessApproval", all, false)
	approve := op(accessApprovePath, "post")
	approve["description"] = "Approve the pending request whose code matches (case, hyphens and spaces ignored; O reads as 0, I and L as 1). Creates the registration for exactly that request's public key and origin; issues no credential. Wrong codes: 404 access_code_invalid, and after 5 in a minute per approver every attempt is 429 too_many_attempts until the window passes. A malformed code is 400 invalid_request and does not count. surface (cli or tui) is accepted only on Local. " + approvers + recheck
	approve["x-callers"] = "approvers"
	add(accessDenyPath, "post", "AccessDenyRequest", "AccessDenial", all, false)
	deny := op(accessDenyPath, "post")
	deny["description"] = "Deny one pending request; its browser's poll then reports denied. 404 access_request_not_found when it is not pending. " + approvers + recheck
	deny["x-callers"] = "approvers"

	add(devicesPath, "get", "", "DeviceList", all, false)
	devices := op(devicesPath, "get")
	devices["description"] = "Browser registrations (devices), most recently used first; current marks the caller's own. " + approvers
	devices["x-callers"] = "approvers"
	add(devicePath, "delete", "", "DeviceRevocation", all, false)
	revoke := op(devicePath, "delete")
	revoke["description"] = "Revoke one browser registration durably: its bearers get 401, its challenges and unused tickets stop working and its terminal and events streams close with 4401. 404 device_not_found for an unknown id. Does not revoke devices it approved. " + approvers + recheck
	revoke["x-callers"] = "approvers"
	revoke["parameters"] = append(revoke["parameters"].([]any), idParam)
}

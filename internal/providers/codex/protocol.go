package codex

import "encoding/json"

// unmarshalParams decodes notification params tolerantly: unknown
// upstream fields are ignored so additive protocol changes do not
// break notification handling.
func unmarshalParams(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// Wire types for the stable app-server surface ai-lb uses. Structs model
// only the fields ai-lb reads; unknown upstream fields are ignored on
// decode so additive protocol changes do not break us.

// ClientInfo identifies ai-lb to the app-server. We never impersonate an
// official client.
type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

func aiLBClientInfo(version string) clientInfo {
	return clientInfo{Name: "ai-lb", Title: "ai-lb", Version: version}
}

type initializeResult struct {
	UserAgent string `json:"userAgent"`
	CodexHome string `json:"codexHome"`
}

// ChatGPTAccount is the account object for type=="chatgpt". Email and
// plan are reported as-is; ai-lb never decodes tokens or infers plans.
type chatGPTAccount struct {
	Type     string  `json:"type"`
	Email    *string `json:"email"`
	PlanType *string `json:"planType"`
}

type accountReadResult struct {
	Account            *accountObject `json:"account"`
	RequiresOpenaiAuth bool           `json:"requiresOpenaiAuth"`
}

// accountObject captures the type discriminator plus optional ChatGPT
// fields. Other account shapes are tolerated and treated as connected
// but undescribed.
type accountObject struct {
	Type     string  `json:"type"`
	Email    *string `json:"email"`
	PlanType *string `json:"planType"`
}

// Login start results.
type loginStartResult struct {
	Type            string  `json:"type"`
	LoginID         string  `json:"loginId"`
	AuthURL         *string `json:"authUrl"`
	VerificationURL *string `json:"verificationUrl"`
	UserCode        *string `json:"userCode"`
}

// Login completion notification params.
type loginCompletedParams struct {
	Success *bool   `json:"success"`
	LoginID *string `json:"loginId"`
	Error   *string `json:"error"`
}

// RateLimitWindow mirrors the upstream window; only UsedPercent is
// required, everything else is optional.
type rateLimitWindow struct {
	UsedPercent        *int   `json:"usedPercent"`
	ResetsAt           *int64 `json:"resetsAt"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
}

type rateLimitSnapshot struct {
	LimitID   *string          `json:"limitId"`
	LimitName *string          `json:"limitName"`
	Primary   *rateLimitWindow `json:"primary"`
	Secondary *rateLimitWindow `json:"secondary"`
	Credits   *creditsSnapshot `json:"credits"`
}

type creditsSnapshot struct {
	Balance    *string `json:"balance"`
	HasCredits *bool   `json:"hasCredits"`
	Unlimited  *bool   `json:"unlimited"`
}

type rateLimitsResult struct {
	AccountID           *string                      `json:"accountId"`
	RateLimits          *rateLimitSnapshot           `json:"rateLimits"`
	RateLimitsByLimitID map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
}

// rpcRequest is one outbound JSON-RPC-ish message.
type rpcRequest struct {
	ID     any    `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// rpcResponse is one inbound correlated response.
type rpcResponse struct {
	ID     any             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// notification is one inbound server notification.
type notification struct {
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	EmittedAt int64           `json:"emittedAtMs"`
}

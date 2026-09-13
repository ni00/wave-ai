// Package auth resolves API keys to (organization, principal) identities.
// The key string itself is never a resource owner; rotation keeps the
// principal stable.
package auth

// Key scopes (OpenAI Agents API borrow: narrow environment keys).
const (
	ScopeAPI = "api" // HTTP API access (default)
)

// Principal identifies an authenticated caller. HTTP middleware stores it in Gin.
type Principal struct {
	OrgID       string
	PrincipalID string
	APIKeyID    string
	Scope       string
}

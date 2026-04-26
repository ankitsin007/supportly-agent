// Package identity is a placeholder for Week 3 work.
//
// In M1 final, the agent exchanges a single-use enrollment token for a
// long-lived agent identity (cert + private key) and rotates every 90
// days. For Week 1 we just use the static API key from config so the
// rest of the pipeline can be exercised end-to-end.
//
// TODO(week-3): implement enrollment-token-for-cert exchange against
// POST /api/v1/agents/enroll on the Supportly side.
package identity

// Identity is the credential the agent presents to Supportly.
// In Week 1 this just wraps the static API key.
type Identity struct {
	APIKey string
}

// FromAPIKey is the M1 stub. Week 3 will add FromEnrollmentToken.
func FromAPIKey(key string) *Identity {
	return &Identity{APIKey: key}
}

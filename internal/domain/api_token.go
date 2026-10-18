package domain

// APIToken identifies a revocable credential belonging to one account.
// The digest is persistence-only; the plaintext value is returned at issuance.
type APIToken struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Label       string `json:"label"`
	Hash        []byte `json:"-"`
	CreatedAtMS int64  `json:"created_at_ms"`
	RevokedAtMS *int64 `json:"revoked_at_ms"`
}

// Live reports whether this independently revocable token is still active.
func (token APIToken) Live() bool { return token.RevokedAtMS == nil }

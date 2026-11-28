// Package strategy owns the dependency-light GoBGP strategy vocabulary.
package strategy

// Strategy identifies a configured BFD-to-BGP action policy.
type Strategy string

const (
	// DisablePeer disables or enables a BGP peer on BFD state changes.
	DisablePeer Strategy = "disable-peer"

	// WithdrawRoutes is reserved for a future route withdrawal strategy. It is
	// recognized for migration diagnostics but is not implemented.
	WithdrawRoutes Strategy = "withdraw-routes"
)

// Parse classifies an exact GoBGP strategy value.
func Parse(value string) (Strategy, bool) {
	strategy := Strategy(value)
	switch strategy {
	case DisablePeer, WithdrawRoutes:
		return strategy, true
	default:
		return "", false
	}
}

// Implemented reports whether the strategy has a production implementation.
func (s Strategy) Implemented() bool {
	return s == DisablePeer
}

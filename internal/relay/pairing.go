package relay

import (
	"crypto/subtle"
	"sync"
	"time"
)

// pairingTTL bounds how long a shown QR stays valid.
const pairingTTL = 5 * time.Minute

// pairings holds the single active one-time pairing secret. Showing a new QR
// replaces the previous secret; a successful pairing consumes it.
type pairings struct {
	mu      sync.Mutex
	secret  []byte
	expires time.Time
}

// New mints a fresh secret, replacing any active one.
func (p *pairings) New() ([]byte, time.Time, error) {
	secret, err := NewPairingSecret()
	if err != nil {
		return nil, time.Time{}, err
	}
	p.mu.Lock()
	p.secret = secret
	p.expires = time.Now().Add(pairingTTL)
	p.mu.Unlock()
	return secret, p.expires, nil
}

// Consume validates a presented secret in constant time and, on success,
// invalidates it (single use).
func (p *pairings) Consume(presented []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.secret == nil || time.Now().After(p.expires) || len(presented) != len(p.secret) {
		return false
	}
	if subtle.ConstantTimeCompare(p.secret, presented) != 1 {
		return false
	}
	p.secret = nil
	return true
}

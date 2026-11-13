// Package clock is the single source of "now" for business code (constitution III).
// Production wires Real in cmd/divya; tests inject a Fake.
package clock

import "time"

// Func returns the current instant. Business code receives one instead of calling time.Now.
type Func = func() time.Time

// Real returns the machine clock in UTC. Only cmd/divya wires it.
func Real() time.Time { return time.Now().UTC() }

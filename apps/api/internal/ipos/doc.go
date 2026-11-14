// Package ipos is the IPO catalogue (feature 008): mainboard IPOs, the APPLY / DO NOT APPLY flag,
// publishing, date-driven lifecycle with admin override, and in-transaction signals (ADR 0008).
// No other module touches the ipos table; they use the exported API and register listeners.
package ipos

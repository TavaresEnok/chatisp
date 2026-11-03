// Package triage decides the arrival menu for one inbox.
// Decide returns declarative actions inside the caller transaction.
// It locks the conversation before any sector share and returns the controller
// epoch and version so a composer can drop actions after a human takeover or a close.
// It does not insert messages, call a channel driver, or assign an attendant.
package triage

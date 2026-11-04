// Package outwebhook delivers conversation and message events to a subscriber.
//
// The event id is stable across retries. The body is signed with HMAC-SHA256
// and does not change between attempts. A failed delivery does not remove the
// local message. The fake receiver accepts each event id once and rejects a replay.
package outwebhook

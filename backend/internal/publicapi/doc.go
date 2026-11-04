// Package publicapi is the provider API.
// It authenticates with a bearer token stored only as a SHA-256 hash.
// Panel cookies are not accepted. Contact creation, text send, assignment,
// state changes and template send stay inside the existing attendance and
// channel-policy checks. Webhooks and ERP calls are outside this package.
package publicapi

// Package publicapi is the read-only provider API.
// It authenticates with a bearer token stored only as a SHA-256 hash.
// Panel cookies are not accepted. Writes, webhooks and ERP calls are outside
// this package.
package publicapi

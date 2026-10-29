// Package metacloud is the WhatsApp Cloud API text driver and webhook decoder.
//
// It speaks the documented Graph messages protocol to an explicitly configured
// endpoint. This package does not dial Meta during tests, does not choose a
// Graph version, and does not resolve a tenant. Tenant routing stays with the
// trusted app–WABA–number registry.
//
// The loopback stand-in (Server) lives in server_test.go. go test compiles
// it; the library does not, and it does not import net/http/httptest.
// It is not Meta, Evolution, or an ERP. Results from a simulation driver
// set Result.Simulation.
package metacloud

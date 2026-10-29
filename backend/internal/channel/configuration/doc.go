// Package configuration is the tenant service for manual Meta Cloud setup.
// It seals secrets with the process crypto.Ring, writes them through the
// attendance store, and resolves routes only from the ingress registry.
// There is no HTTP handler in this package. Panel methods take a session
// hash; server methods are in-process and are not fed by an external payload.
package configuration

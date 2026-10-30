// Package service is the M2 attendance application over store, the pure
// conversation model, and River InsertTx.
//
// A panel call opens one db.WithTenantTx, rechecks the live principal, and
// commits the message, command, outbox, realtime event and job together.
// Nothing in this package dials Meta, Evolution, the ERP, or any other network.
// Workers call the reserve, result, receipt and reconcile methods instead of
// issuing their own SQL. A worker result must present the reserve fence.
// A remote observation does not. Sector membership does not exist until M3: a permitted
// inbox is an inbox of the active tenant. Sending also requires the Meta
// connector to be enabled and connected.
package service

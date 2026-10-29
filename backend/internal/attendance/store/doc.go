// Package store is the M2 persistence boundary for attendance.
// Callers pass a transaction from db.WithTenantTx or db.WithIngressTx.
// The tenant is the one pinned on that transaction. This package does not
// read a request body, does not authorize HTTP roles, and does not import River.
// A later worker can pass the same pgx.Tx to River.
package store

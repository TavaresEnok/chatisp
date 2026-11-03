// Package distribution assigns one queued conversation by round-robin.
// Pick reads presence.Effective, keeps the sector and inbox union, and reserves
// capacity with attendance/store.LockAssignmentCapacity in the same transaction.
// A sector with automatic assignment disabled stays in the queue.
// It does not open HTTP, read the global users table, or call an external provider.
package distribution

// Package lifecycle stores human snooze and inactivity watches.
//
// Snooze is not a conversation status and does not close the attendance.
// Jobs receive the caller's clock and revalidate inside the caller's transaction.
// ReturnDue locks one conversation per transaction. A warning becomes sent only
// through ConfirmWarning for that generation. Jobs are not registered with
// platform/jobs. Composition wires HTTP and the worker later.
package lifecycle

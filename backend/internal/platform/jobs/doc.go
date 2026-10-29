// Package jobs is the River adapter for Chat ISP.
//
// It enqueues on the caller transaction, runs the open-source client, and
// applies River migrations only through the owner path. Handlers are injected
// by later packages. This package does not start a process-wide runtime and
// does not call Meta, Evolution or an ERP.
package jobs

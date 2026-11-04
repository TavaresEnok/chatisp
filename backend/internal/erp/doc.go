// Package erp is the internal ERP contract and the identified fake adapter.
//
// Names such as SearchCustomer are internal capabilities, not supplier endpoints.
// The fake is deterministic and always marked as a simulation. A missing
// capability, a configured outage or an open circuit returns unavailable and
// does not fail the local attendance reply. An ambiguous external result is
// stored once and is not executed again for the same local key.
package erp

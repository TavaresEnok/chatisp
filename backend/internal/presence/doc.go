// Package presence aggregates attendant availability from panel tabs.
// Heartbeats live in PostgreSQL. Closing one tab does not mark offline an
// attendant who still has another live tab. Manual busy overrides available.
// Offline tolerance is calculated and exposed for a later unassignment job;
// this package never changes conversations, capacity or assignment.
package presence

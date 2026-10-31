package httpapi

import "chatisp/internal/attendance/store"

type queueInput struct {
	Credentials
	Tab      string `query:"tab"`
	Order    string `query:"order"`
	Status   string `query:"status"`
	Sector   string `query:"sector"`
	Assignee string `query:"assignee"`
	Inbox    string `query:"inbox"`
	Tag      string `query:"tag"`
	From     string `query:"from"`
	To       string `query:"to"`
	Search   string `query:"search"`
	Limit    int    `query:"limit"`
	Cursor   string `query:"page_cursor"`
}
type queueOutput struct{ Body store.QueuePage }

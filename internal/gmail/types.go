// internal/gmail/types.go
package gmail

import "time"

type MessageID string
type LabelID string

type Header struct {
	Name  string
	Value string
}

type MessageMeta struct {
	ID      MessageID
	Labels  []LabelID
	Headers map[string]string // From, To, Subject, List-Id, Auto-Submitted, Precedence, Date, etc.
	Date    time.Time
}

type ModifyOps struct {
	AddLabels    []LabelID
	RemoveLabels []LabelID
	MarkRead     bool // implies removing UNREAD
	Archive      bool // implies removing INBOX
}

type Query struct {
	Raw string // Gmail query string, already formed (e.g., `in:inbox is:unread before:1726440000 -is:starred -label:"team"`)
}

package gmail

import "time"

// MessageID uniquely identifies a Gmail message.
type MessageID string

// LabelID identifies a Gmail label.
type LabelID string

// Query represents a raw Gmail search query string.
type Query struct {
	Raw string
}

// MessageMeta captures metadata for a Gmail message that is safe to fetch quickly.
type MessageMeta struct {
	ID       MessageID
	LabelIDs []LabelID
	Headers  map[string]string
	Date     time.Time
}

// ModifyOps describes the label mutations to apply to a set of messages.
type ModifyOps struct {
	AddLabels    []LabelID
	RemoveLabels []LabelID
	MarkRead     bool
	Archive      bool
}

// ListPage contains a set of message IDs and a pagination token for the next page.
type ListPage struct {
	IDs           []MessageID
	NextPageToken string
}

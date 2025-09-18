// internal/gmail/client.go
package gmail

import "context"

// Client is a narrow interface that's easy to fake in tests.
type Client interface {
	List(ctx context.Context, q Query, pageSize int) (ids []MessageID, nextPageToken string, _ error)
	GetMetadata(ctx context.Context, id MessageID, headers []string) (MessageMeta, error)
	BatchModify(ctx context.Context, ids []MessageID, ops ModifyOps) error
	ListLabels(ctx context.Context) (byName map[string]LabelID, byID map[LabelID]string, err error)
	EnsureLabel(ctx context.Context, name string) (LabelID, error)
	// Optional: Export/Compile gmailctl if you decide to shell out.
}

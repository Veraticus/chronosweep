package gmail

import "context"

// Client is the narrow Gmail surface required by chronosweep.
type Client interface {
	List(ctx context.Context, q Query, pageToken string, pageSize int) (ListPage, error)
	GetMetadata(ctx context.Context, id MessageID, headers []string) (MessageMeta, error)
	BatchModify(ctx context.Context, ids []MessageID, ops ModifyOps) error
	ListLabels(ctx context.Context) (map[string]LabelID, map[LabelID]string, error)
	EnsureLabel(ctx context.Context, name string) (LabelID, error)
}

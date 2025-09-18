package runtime

import (
	"context"
	"fmt"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"

	"github.com/joshsymonds/chronosweep/internal/gmail"
)

// ClientAdapter implements gmail.Client using the Google API client.
type ClientAdapter struct {
	svc *gmailapi.Service
}

// NewGoogleAPIClient wraps a gmail Service with the chronosweep gmail.Client interface.
func NewGoogleAPIClient(svc *gmailapi.Service) *ClientAdapter {
	return &ClientAdapter{svc: svc}
}

// List retrieves message identifiers matching the supplied query.
func (g *ClientAdapter) List(
	ctx context.Context,
	q gmail.Query,
	pageToken string,
	pageSize int,
) (gmail.ListPage, error) {
	call := g.svc.Users.Messages.List("me").Q(q.Raw)
	if pageSize > 0 {
		call = call.MaxResults(int64(pageSize))
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	res, err := call.Context(ctx).Do()
	if err != nil {
		return gmail.ListPage{}, fmt.Errorf("list messages: %w", err)
	}
	ids := make([]gmail.MessageID, 0, len(res.Messages))
	for _, msg := range res.Messages {
		ids = append(ids, gmail.MessageID(msg.Id))
	}
	return gmail.ListPage{IDs: ids, NextPageToken: res.NextPageToken}, nil
}

// GetMetadata fetches metadata headers for a specific message.
func (g *ClientAdapter) GetMetadata(
	ctx context.Context,
	id gmail.MessageID,
	headers []string,
) (gmail.MessageMeta, error) {
	call := g.svc.Users.Messages.Get("me", string(id)).
		Format("metadata").
		MetadataHeaders(headers...)
	msg, err := call.Context(ctx).Do()
	if err != nil {
		return gmail.MessageMeta{}, fmt.Errorf("get metadata %s: %w", id, err)
	}
	headersMap := make(map[string]string, len(msg.Payload.Headers))
	for _, h := range msg.Payload.Headers {
		headersMap[h.Name] = h.Value
	}
	meta := gmail.MessageMeta{
		ID:       id,
		LabelIDs: toLabelIDs(msg.LabelIds),
		Headers:  headersMap,
		Date:     time.UnixMilli(msg.InternalDate),
	}
	return meta, nil
}

// BatchModify applies label modifications to the provided message IDs.
func (g *ClientAdapter) BatchModify(
	ctx context.Context,
	ids []gmail.MessageID,
	ops gmail.ModifyOps,
) error {
	if len(ids) == 0 {
		return nil
	}
	req := &gmailapi.BatchModifyMessagesRequest{
		Ids: toStrings(ids),
	}
	add := make([]string, 0, len(ops.AddLabels))
	for _, lid := range ops.AddLabels {
		add = append(add, string(lid))
	}
	remove := make([]string, 0, len(ops.RemoveLabels))
	for _, lid := range ops.RemoveLabels {
		remove = append(remove, string(lid))
	}
	if ops.MarkRead {
		remove = append(remove, "UNREAD")
	}
	if ops.Archive {
		remove = append(remove, "INBOX")
	}
	if len(add) > 0 {
		req.AddLabelIds = add
	}
	if len(remove) > 0 {
		req.RemoveLabelIds = remove
	}
	if err := g.svc.Users.Messages.BatchModify("me", req).Context(ctx).Do(); err != nil {
		return fmt.Errorf("batch modify: %w", err)
	}
	return nil
}

// ListLabels returns Gmail labels keyed by both name and identifier.
func (g *ClientAdapter) ListLabels(
	ctx context.Context,
) (map[string]gmail.LabelID, map[gmail.LabelID]string, error) {
	res, err := g.svc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("list labels: %w", err)
	}
	byName := make(map[string]gmail.LabelID, len(res.Labels))
	byID := make(map[gmail.LabelID]string, len(res.Labels))
	for _, lbl := range res.Labels {
		id := gmail.LabelID(lbl.Id)
		byName[lbl.Name] = id
		byID[id] = lbl.Name
	}
	return byName, byID, nil
}

// EnsureLabel guarantees that the requested label exists, creating it when necessary.
func (g *ClientAdapter) EnsureLabel(ctx context.Context, name string) (gmail.LabelID, error) {
	byName, _, err := g.ListLabels(ctx)
	if err != nil {
		return "", err
	}
	if id, ok := byName[name]; ok {
		return id, nil
	}
	created, err := g.svc.Users.Labels.Create("me", &gmailapi.Label{Name: name}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create label %q: %w", name, err)
	}
	return gmail.LabelID(created.Id), nil
}

func toStrings(ids []gmail.MessageID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

func toLabelIDs(ids []string) []gmail.LabelID {
	out := make([]gmail.LabelID, 0, len(ids))
	for _, id := range ids {
		out = append(out, gmail.LabelID(id))
	}
	return out
}

var _ gmail.Client = (*ClientAdapter)(nil)

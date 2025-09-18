// internal/runtime/googleapi.go — adapts *gmail.Service to our small interface
package runtime

import (
	"context"
	"fmt"

	"google.golang.org/api/gmail/v1"

	gc "github.com/yourorg/chronosweep/internal/gmail"
)

type googleClient struct{ svc *gmail.Service }

func NewGoogleAPIClient(svc *gmail.Service) *googleClient { return &googleClient{svc} }

func (g *googleClient) List(ctx context.Context, q gc.Query, pageSize int) ([]gc.MessageID, string, error) {
	call := g.svc.Users.Messages.List("me").Q(q.Raw).MaxResults(int64(pageSize))
	res, err := call.Context(ctx).Do()
	if err != nil {
		return nil, "", err
	}
	var ids []gc.MessageID
	for _, m := range res.Messages {
		ids = append(ids, gc.MessageID(m.Id))
	}
	return ids, res.NextPageToken, nil
}

func (g *googleClient) GetMetadata(ctx context.Context, id gc.MessageID, headers []string) (gc.MessageMeta, error) {
	msg, err := g.svc.Users.Messages.Get("me", string(id)).Format("metadata").MetadataHeaders(headers...).Context(ctx).Do()
	if err != nil {
		return gc.MessageMeta{}, err
	}
	h := map[string]string{}
	for _, hd := range msg.Payload.Headers {
		h[hd.Name] = hd.Value
	}
	return gc.MessageMeta{ID: id, Headers: h, Labels: toLabelIDs(msg.LabelIds)}, nil
}

func (g *googleClient) BatchModify(ctx context.Context, ids []gc.MessageID, ops gc.ModifyOps) error {
	req := &gmail.BatchModifyMessagesRequest{Ids: toStrings(ids)}
	if len(ops.AddLabels) > 0 {
		req.AddLabelIds = toStringsL(ops.AddLabels)
	}
	if len(ops.RemoveLabels) > 0 {
		req.RemoveLabelIds = toStringsL(ops.RemoveLabels)
	}
	_, err := g.svc.Users.Messages.BatchModify("me", req).Context(ctx).Do()
	return err
}

func (g *googleClient) ListLabels(ctx context.Context) (map[string]gc.LabelID, map[gc.LabelID]string, error) {
	lr, err := g.svc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return nil, nil, err
	}
	byName := map[string]gc.LabelID{}
	byID := map[gc.LabelID]string{}
	for _, l := range lr.Labels {
		byName[l.Name] = gc.LabelID(l.Id)
		byID[gc.LabelID(l.Id)] = l.Name
	}
	return byName, byID, nil
}

func (g *googleClient) EnsureLabel(ctx context.Context, name string) (gc.LabelID, error) {
	byName, _, err := g.ListLabels(ctx)
	if err != nil {
		return "", err
	}
	if id, ok := byName[name]; ok {
		return id, nil
	}
	created, err := g.svc.Users.Labels.Create("me", &gmail.Label{Name: name}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create label %q: %w", name, err)
	}
	return gc.LabelID(created.Id), nil
}

// helpers toStrings, toStringsL, toLabelIDs omitted for brevity

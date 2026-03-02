package peerserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

var _ BlobSource = &metadataSource{}

// metadataSource serves serialized template metadata (metadata.json).
type metadataSource struct {
	getMetadata func() (metadata.Template, error)
}

func (f *metadataSource) Exists(_ context.Context) (bool, error) {
	return false, ErrNotSupported
}

func (f *metadataSource) Stream(ctx context.Context, sender Sender) error {
	_, span := tracer.Start(ctx, "stream-metadata")
	defer span.End()

	meta, err := f.getMetadata()
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("get metadata: %w", err)
	}

	data, err := json.Marshal(meta)
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("serialize metadata: %w", err)
	}

	return sender.Send(data)
}

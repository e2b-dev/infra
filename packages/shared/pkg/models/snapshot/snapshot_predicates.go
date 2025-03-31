package snapshot

import (
	"encoding/json"

	"entgo.io/ent/dialect/sql"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/predicate"
	"go.uber.org/zap"
)

func MetadataContains(metadata map[string]string) predicate.Snapshot {
	return predicate.Snapshot(func(s *sql.Selector) {
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			zap.L().Error("Error marshaling metadata to JSON", zap.Error(err))
			s.Where(sql.False())
			return
		}

		// Use the JSONB containment operator (@>) to check if the metadata field contains all key-value pairs
		// This effectively checks for equality when we're matching against the entire metadata object
		s.Where(sql.P(func(b *sql.Builder) {
			b.Ident(s.C(FieldMetadata)).WriteString(" @> ").Arg(string(metadataJSON))
		}))
	})
}

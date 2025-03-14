package snapshot

import (
	"encoding/json"
	"log"

	"entgo.io/ent/dialect/sql"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/predicate"
)

func MetadataEq(metadata map[string]string) predicate.Snapshot {
	return predicate.Snapshot(func(s *sql.Selector) {
		// Convert the metadata map to JSON
		metadataJSON, err := json.Marshal(metadata)
		if err != nil {
			// Log the error but continue with an empty predicate
			// This will effectively match no records if there's a marshaling error
			log.Printf("Error marshaling metadata to JSON: %v", err)
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

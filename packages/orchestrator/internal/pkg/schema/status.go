package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// The Status table stores the global status of the
// orchestrator. Exists to have a global version/clock
type Status struct {
	ent.Schema
}

func (Status) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("version").NonNegative().Unique(),
		field.Time("updated_at").Default(time.Now),
		field.Enum("status").Values("initializing", "running", "draining", "terminating").Default("running"),
	}
}

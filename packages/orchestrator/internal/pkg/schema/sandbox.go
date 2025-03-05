package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
)

type Sandbox struct {
	ent.Schema
}

func (Sandbox) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Unique().Immutable().NotEmpty(),
		field.Time("started_at").Immutable().Default(time.Now).
			Annotations(
				entsql.Default("CURRENT_TIMESTAMP"),
			),
		field.Time("updated_at").Default(time.Now),
		field.Time("terminated_at").Nillable(),
		field.Time("deadline").Nillable(),
		field.Enum("status").Values("pending", "running", "paused", "terminated").Default("pending"),
		field.Int64("duration_ms").NonNegative(),
		field.Int64("version").NonNegative().Comment("an incrementing clock of this "),
		field.Int64("global_version").NonNegative().Comment("a record of the version of the global state of the last modification."),
		// TODO: should we store more data persistently about sandboxes?
		// TODO: mechanism to cleanup
	}
}

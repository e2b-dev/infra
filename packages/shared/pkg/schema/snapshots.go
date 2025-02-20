package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type Snapshot struct {
	ent.Schema
}

func (Snapshot) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Immutable().Unique().Annotations(entsql.Default("gen_random_uuid()")),
		field.Time("created_at").Immutable().Default(time.Now).
			Annotations(
				entsql.Default("CURRENT_TIMESTAMP"),
			),
		field.Time("sandbox_started_at"),
		field.String("base_env_id").SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("env_id").SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("sandbox_id").Unique().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.JSON("metadata", map[string]string{}).SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
	}
}

func (Snapshot) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("env", Env.Type).Ref("snapshots").Unique().Field("env_id").Required(),
	}
}

func (Snapshot) Mixin() []ent.Mixin {
	return []ent.Mixin{
		Mixin{},
	}
}

package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type TeamAPIKey struct {
	ent.Schema
}

func (TeamAPIKey) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Immutable().Unique().Annotations(entsql.Default("gen_random_uuid()")),
		field.String("api_key").Unique().Sensitive().SchemaType(map[string]string{dialect.Postgres: "character varying(44)"}),
		field.String("api_key_hash").Unique().Sensitive().SchemaType(map[string]string{dialect.Postgres: "character varying(64)"}),
		field.String("api_key_mask").SchemaType(map[string]string{dialect.Postgres: "character varying(44)"}),
		field.Time("created_at").Immutable().Default(time.Now).Annotations(
			entsql.Default("CURRENT_TIMESTAMP"),
		),
		field.Time("updated_at").Nillable().Optional(),
		field.UUID("team_id", uuid.UUID{}),
		field.String("name").SchemaType(map[string]string{dialect.Postgres: "text"}).Default("Unnamed API Key"),
		field.UUID("created_by", uuid.UUID{}).Nillable().Optional(),
		field.Time("last_used").Nillable().Optional(),
	}
}

func (TeamAPIKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("team", Team.Type).Unique().Required().
			Ref("team_api_keys").
			Field("team_id"),
		edge.From("creator", User.Type).Unique().
			Ref("created_api_keys").Field("created_by"),
	}
}

func (TeamAPIKey) Annotations() []schema.Annotation {
	return nil
}

func (TeamAPIKey) Mixin() []ent.Mixin {
	return []ent.Mixin{
		Mixin{},
	}
}

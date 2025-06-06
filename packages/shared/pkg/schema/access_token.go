package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type AccessToken struct {
	ent.Schema
}

func (AccessToken) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Immutable().Unique().Annotations(entsql.Default("gen_random_uuid()")),
		field.String("access_token").Unique().Immutable().Sensitive().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("access_token_hash").Immutable().Unique().Sensitive().SchemaType(map[string]string{dialect.Postgres: "text"}),

		field.String("access_token_prefix").Immutable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int("access_token_length").Immutable(),
		field.String("access_token_mask_prefix").Immutable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("access_token_mask_suffix").Immutable().SchemaType(map[string]string{dialect.Postgres: "text"}),

		field.String("name").SchemaType(map[string]string{dialect.Postgres: "text"}).Default("Unnamed Access Token"),
		field.UUID("user_id", uuid.UUID{}),
		field.Time("created_at").Optional().Immutable().Annotations(
			entsql.Default("CURRENT_TIMESTAMP"),
		),
	}
}

func (AccessToken) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("access_tokens").Unique().Field("user_id").Required(),
	}
}

func (AccessToken) Mixin() []ent.Mixin {
	return []ent.Mixin{
		Mixin{},
	}
}

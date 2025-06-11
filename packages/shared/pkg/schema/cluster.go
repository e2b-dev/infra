package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type Cluster struct {
	ent.Schema
}

func (Cluster) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Immutable().Unique().Annotations(entsql.Default("gen_random_uuid()")),
		field.String("endpoint").SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("token").SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (Cluster) Edges() []ent.Edge {
	return []ent.Edge{}
}

func (Cluster) Mixin() []ent.Mixin {
	return []ent.Mixin{
		Mixin{},
	}
}

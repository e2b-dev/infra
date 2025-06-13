package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Cluster holds the schema definition for the Cluster entity.
type Cluster struct {
	ent.Schema
}

// Fields of the Cluster.
func (Cluster) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Immutable().
			Unique().
			Annotations(entsql.Default("gen_random_uuid()")),
		field.String("endpoint").
			NotEmpty().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("token").
			NotEmpty().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
	}
}

func (Cluster) Edges() []ent.Edge {
	return []ent.Edge{}
}

func (Cluster) Annotations() []schema.Annotation {
	withComments := true

	return []schema.Annotation{
		entsql.Annotation{WithComments: &withComments},
	}
}

func (Cluster) Mixin() []ent.Mixin {
	return []ent.Mixin{
		Mixin{},
	}
}

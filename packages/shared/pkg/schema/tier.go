package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Tier struct {
	ent.Schema
}

func (Tier) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable().Unique().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("name").SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int64("disk_mb").Annotations(entsql.Check("disk_mb > 0"), entsql.Default("512")),
		field.Int64("concurrent_instances").Annotations(entsql.Check("concurrent_instances > 0")).Comment("The number of instances the team can run concurrently"),
		field.Int64("concurrent_template_builds").Annotations(entsql.Check("concurrent_template_builds > 0")).Comment("The number of concurrent template builds the team can run"),
		field.Int64("max_length_hours"),
	}
}

func (Tier) Annotations() []schema.Annotation {
	withComments := true
	return []schema.Annotation{
		entsql.Annotation{
			WithComments: &withComments,
			Checks: map[string]string{
				"tiers_concurrent_sessions_check":        "concurrent_instances > 0",
				"tiers_concurrent_template_builds_check": "concurrent_template_builds > 0",
				"tiers_disk_mb_check":                    "disk_mb > 0",
			},
		},
	}
}

func (Tier) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("teams", Team.Type),
	}
}

func (Tier) Mixin() []ent.Mixin {
	return []ent.Mixin{
		Mixin{},
	}
}

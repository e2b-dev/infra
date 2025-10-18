package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// User holds the schema definition for the User entity.
type User struct{ ent.Schema }

// Fields of the User.
func (User) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Immutable().Unique().Annotations(entsql.Default("gen_random_uuid()")),
		field.String("email").MaxLen(255).SchemaType(map[string]string{dialect.Postgres: "character varying(255)"}),
	}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Schema("auth"),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("teams", Team.Type).Through("users_teams", UsersTeams.Type).Ref("users"),
		edge.To("created_envs", Env.Type).Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("access_tokens", AccessToken.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

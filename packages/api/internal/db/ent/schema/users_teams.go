package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type UsersTeams struct {
	ent.Schema
}

// Fields of the UsersTeams.
func (UsersTeams) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("user_id", uuid.UUID{}),
		field.UUID("team_id", uuid.UUID{}),
	}
}

// Edges of the UsersTeams.
func (UsersTeams) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("users", User.Type).
			Required().
			Unique().
			Field("user_id"),
		edge.To("teams", Team.Type).
			Required().
			Unique().
			Field("team_id"),
	}
}

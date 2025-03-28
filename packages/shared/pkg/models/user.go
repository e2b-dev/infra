// Code generated by ent, DO NOT EDIT.

package models

import (
	"fmt"
	"strings"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/user"
	"github.com/google/uuid"
)

// User is the model entity for the User schema.
type User struct {
	config `json:"-"`
	// ID of the ent.
	ID uuid.UUID `json:"id,omitempty"`
	// Email holds the value of the "email" field.
	Email string `json:"email,omitempty"`
	// Edges holds the relations/edges for other nodes in the graph.
	// The values are being populated by the UserQuery when eager-loading is set.
	Edges        UserEdges `json:"edges"`
	selectValues sql.SelectValues
}

// UserEdges holds the relations/edges for other nodes in the graph.
type UserEdges struct {
	// Teams holds the value of the teams edge.
	Teams []*Team `json:"teams,omitempty"`
	// CreatedEnvs holds the value of the created_envs edge.
	CreatedEnvs []*Env `json:"created_envs,omitempty"`
	// AccessTokens holds the value of the access_tokens edge.
	AccessTokens []*AccessToken `json:"access_tokens,omitempty"`
	// CreatedAPIKeys holds the value of the created_api_keys edge.
	CreatedAPIKeys []*TeamAPIKey `json:"created_api_keys,omitempty"`
	// UsersTeams holds the value of the users_teams edge.
	UsersTeams []*UsersTeams `json:"users_teams,omitempty"`
	// loadedTypes holds the information for reporting if a
	// type was loaded (or requested) in eager-loading or not.
	loadedTypes [5]bool
}

// TeamsOrErr returns the Teams value or an error if the edge
// was not loaded in eager-loading.
func (e UserEdges) TeamsOrErr() ([]*Team, error) {
	if e.loadedTypes[0] {
		return e.Teams, nil
	}
	return nil, &NotLoadedError{edge: "teams"}
}

// CreatedEnvsOrErr returns the CreatedEnvs value or an error if the edge
// was not loaded in eager-loading.
func (e UserEdges) CreatedEnvsOrErr() ([]*Env, error) {
	if e.loadedTypes[1] {
		return e.CreatedEnvs, nil
	}
	return nil, &NotLoadedError{edge: "created_envs"}
}

// AccessTokensOrErr returns the AccessTokens value or an error if the edge
// was not loaded in eager-loading.
func (e UserEdges) AccessTokensOrErr() ([]*AccessToken, error) {
	if e.loadedTypes[2] {
		return e.AccessTokens, nil
	}
	return nil, &NotLoadedError{edge: "access_tokens"}
}

// CreatedAPIKeysOrErr returns the CreatedAPIKeys value or an error if the edge
// was not loaded in eager-loading.
func (e UserEdges) CreatedAPIKeysOrErr() ([]*TeamAPIKey, error) {
	if e.loadedTypes[3] {
		return e.CreatedAPIKeys, nil
	}
	return nil, &NotLoadedError{edge: "created_api_keys"}
}

// UsersTeamsOrErr returns the UsersTeams value or an error if the edge
// was not loaded in eager-loading.
func (e UserEdges) UsersTeamsOrErr() ([]*UsersTeams, error) {
	if e.loadedTypes[4] {
		return e.UsersTeams, nil
	}
	return nil, &NotLoadedError{edge: "users_teams"}
}

// scanValues returns the types for scanning values from sql.Rows.
func (*User) scanValues(columns []string) ([]any, error) {
	values := make([]any, len(columns))
	for i := range columns {
		switch columns[i] {
		case user.FieldEmail:
			values[i] = new(sql.NullString)
		case user.FieldID:
			values[i] = new(uuid.UUID)
		default:
			values[i] = new(sql.UnknownType)
		}
	}
	return values, nil
}

// assignValues assigns the values that were returned from sql.Rows (after scanning)
// to the User fields.
func (u *User) assignValues(columns []string, values []any) error {
	if m, n := len(values), len(columns); m < n {
		return fmt.Errorf("mismatch number of scan values: %d != %d", m, n)
	}
	for i := range columns {
		switch columns[i] {
		case user.FieldID:
			if value, ok := values[i].(*uuid.UUID); !ok {
				return fmt.Errorf("unexpected type %T for field id", values[i])
			} else if value != nil {
				u.ID = *value
			}
		case user.FieldEmail:
			if value, ok := values[i].(*sql.NullString); !ok {
				return fmt.Errorf("unexpected type %T for field email", values[i])
			} else if value.Valid {
				u.Email = value.String
			}
		default:
			u.selectValues.Set(columns[i], values[i])
		}
	}
	return nil
}

// Value returns the ent.Value that was dynamically selected and assigned to the User.
// This includes values selected through modifiers, order, etc.
func (u *User) Value(name string) (ent.Value, error) {
	return u.selectValues.Get(name)
}

// QueryTeams queries the "teams" edge of the User entity.
func (u *User) QueryTeams() *TeamQuery {
	return NewUserClient(u.config).QueryTeams(u)
}

// QueryCreatedEnvs queries the "created_envs" edge of the User entity.
func (u *User) QueryCreatedEnvs() *EnvQuery {
	return NewUserClient(u.config).QueryCreatedEnvs(u)
}

// QueryAccessTokens queries the "access_tokens" edge of the User entity.
func (u *User) QueryAccessTokens() *AccessTokenQuery {
	return NewUserClient(u.config).QueryAccessTokens(u)
}

// QueryCreatedAPIKeys queries the "created_api_keys" edge of the User entity.
func (u *User) QueryCreatedAPIKeys() *TeamAPIKeyQuery {
	return NewUserClient(u.config).QueryCreatedAPIKeys(u)
}

// QueryUsersTeams queries the "users_teams" edge of the User entity.
func (u *User) QueryUsersTeams() *UsersTeamsQuery {
	return NewUserClient(u.config).QueryUsersTeams(u)
}

// Update returns a builder for updating this User.
// Note that you need to call User.Unwrap() before calling this method if this User
// was returned from a transaction, and the transaction was committed or rolled back.
func (u *User) Update() *UserUpdateOne {
	return NewUserClient(u.config).UpdateOne(u)
}

// Unwrap unwraps the User entity that was returned from a transaction after it was closed,
// so that all future queries will be executed through the driver which created the transaction.
func (u *User) Unwrap() *User {
	_tx, ok := u.config.driver.(*txDriver)
	if !ok {
		panic("models: User is not a transactional entity")
	}
	u.config.driver = _tx.drv
	return u
}

// String implements the fmt.Stringer.
func (u *User) String() string {
	var builder strings.Builder
	builder.WriteString("User(")
	builder.WriteString(fmt.Sprintf("id=%v, ", u.ID))
	builder.WriteString("email=")
	builder.WriteString(u.Email)
	builder.WriteByte(')')
	return builder.String()
}

// Users is a parsable slice of User.
type Users []*User

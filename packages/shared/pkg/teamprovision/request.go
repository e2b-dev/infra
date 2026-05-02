package teamprovision

import "github.com/google/uuid"

const (
	ReasonDefaultSignupTeam = "default_signup_team"
	ReasonAdditionalTeam    = "additional_team"
)

const (
	AuthMethodPassword = "Password"
	AuthMethodSocial   = "Social"
)

type CreatorContextV1 struct {
	IPAddress  string `json:"ip_address,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"`
}

type TeamBillingProvisionRequestedV1 struct {
	TeamID         uuid.UUID         `json:"team_id"`
	TeamName       string            `json:"team_name"`
	TeamEmail      string            `json:"team_email"`
	CreatorUserID  uuid.UUID         `json:"creator_user_id"`
	CreatorContext *CreatorContextV1 `json:"creator_context,omitempty"`
	Reason         string            `json:"reason"`
}

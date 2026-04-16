package teamprovision

import "github.com/google/uuid"

const (
	ReasonDefaultSignupTeam = "default_signup_team"
	ReasonAdditionalTeam    = "additional_team"
)

type TeamBillingProvisionRequestedV1 struct {
	TeamID      uuid.UUID `json:"team_id"`
	TeamName    string    `json:"team_name"`
	TeamEmail   string    `json:"team_email"`
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	Reason      string    `json:"reason"`
}

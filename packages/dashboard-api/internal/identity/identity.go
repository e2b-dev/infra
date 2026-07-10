// Package identity abstracts external identity providers behind a
// subject-keyed Directory, an issuer-aware Linkage over user_identities, and a
// Service that routes user-keyed operations to the directory registered for
// each linked issuer.
package identity

import (
	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type Identity struct {
	Subject           string
	Email             string
	Name              string
	ProfilePictureURL string
	Providers         []string
	OrganizationID    uuid.UUID
	SignupIP          string
	SignupUserAgent   string
	AuthMethod        string
}

type Profile struct {
	UserID            uuid.UUID
	Email             string
	Name              string
	ProfilePictureURL string
	Providers         []string
}

func ProfileFromIdentity(userID uuid.UUID, id Identity) Profile {
	return Profile{
		UserID:            userID,
		Email:             id.Email,
		Name:              id.Name,
		ProfilePictureURL: id.ProfilePictureURL,
		Providers:         id.Providers,
	}
}

func CreatorContextFromIdentity(id Identity) *sharedteamprovision.CreatorContextV1 {
	return &sharedteamprovision.CreatorContextV1{
		IPAddress:  id.SignupIP,
		UserAgent:  id.SignupUserAgent,
		AuthMethod: id.AuthMethod,
	}
}

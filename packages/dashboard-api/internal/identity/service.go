package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type Service interface {
	IdentityOrganizationID(ctx context.Context, issuer, subject string) (uuid.UUID, error)
	SetIdentityExternalID(ctx context.Context, issuer, subject string, externalID uuid.UUID) error
	ProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error)
	UserOrganizationID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	TeamCreatorContext(ctx context.Context, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error)
	FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error)
	PrepareDeleteUser(ctx context.Context, userID uuid.UUID) (DeleteUserHandle, error)
}

type service struct {
	directories map[string]Directory
	issuers     []string
	linkage     Linkage
}

func NewService(directories map[string]Directory, linkage Linkage) (Service, error) {
	if len(directories) == 0 {
		return nil, errors.New("at least one identity directory is required")
	}
	if linkage == nil {
		return nil, errors.New("identity linkage is required")
	}

	registry := make(map[string]Directory, len(directories))
	issuers := make([]string, 0, len(directories))
	for issuer, directory := range directories {
		issuer = strings.TrimSpace(issuer)
		if issuer == "" {
			return nil, errors.New("identity directory issuer must not be empty")
		}
		if directory == nil {
			return nil, fmt.Errorf("identity directory for issuer %q is nil", issuer)
		}
		registry[issuer] = directory
		issuers = append(issuers, issuer)
	}
	slices.Sort(issuers)

	return &service{
		directories: registry,
		issuers:     issuers,
		linkage:     linkage,
	}, nil
}

func (s *service) directoryForIssuer(issuer string) (Directory, error) {
	directory, ok := s.directories[strings.TrimSpace(issuer)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownIssuer, issuer)
	}

	return directory, nil
}

func (s *service) IdentityOrganizationID(ctx context.Context, issuer, subject string) (uuid.UUID, error) {
	directory, err := s.directoryForIssuer(issuer)
	if err != nil {
		return uuid.Nil, err
	}

	id, err := directory.GetIdentity(ctx, subject)
	if err != nil {
		if errors.Is(err, ErrIdentityNotFound) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}

	return id.OrganizationID, nil
}

func (s *service) SetIdentityExternalID(ctx context.Context, issuer, subject string, externalID uuid.UUID) error {
	directory, err := s.directoryForIssuer(issuer)
	if err != nil {
		return err
	}

	return directory.SetExternalID(ctx, subject, externalID)
}

func (s *service) ProfilesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
	identities, err := s.identitiesByUserID(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	profiles := make(map[uuid.UUID]Profile, len(identities))
	for userID, id := range identities {
		profiles[userID] = ProfileFromIdentity(userID, id)
	}

	return profiles, nil
}

func (s *service) UserOrganizationID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	if userID == uuid.Nil {
		return uuid.Nil, nil
	}

	linked, err := s.linkedIdentitiesForUsers(ctx, []uuid.UUID{userID})
	if err != nil {
		return uuid.Nil, err
	}
	if len(linked) == 0 {
		return uuid.Nil, nil
	}

	byIssuer := make(map[string][]LinkedIdentity, len(s.issuers))
	for _, row := range linked {
		byIssuer[row.Issuer] = append(byIssuer[row.Issuer], row)
	}

	var orgID uuid.UUID
	for _, issuer := range s.issuers {
		rows, ok := byIssuer[issuer]
		if !ok {
			continue
		}

		directory, err := s.directoryForIssuer(issuer)
		if err != nil {
			return uuid.Nil, err
		}

		subjects := make([]string, 0, len(rows))
		for _, row := range rows {
			subjects = append(subjects, row.Subject)
		}

		identities, err := directory.ListIdentities(ctx, subjects)
		if err != nil {
			return uuid.Nil, err
		}

		for _, identity := range identities {
			if identity.OrganizationID == uuid.Nil {
				continue
			}
			if orgID != uuid.Nil && orgID != identity.OrganizationID {
				return uuid.Nil, fmt.Errorf("multiple SSO organizations for user %s", userID)
			}
			orgID = identity.OrganizationID
		}
	}

	return orgID, nil
}

func (s *service) TeamCreatorContext(ctx context.Context, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	if userID == uuid.Nil {
		return nil, nil
	}

	identities, err := s.identitiesByUserID(ctx, []uuid.UUID{userID})
	if err != nil {
		return nil, err
	}

	id, ok := identities[userID]
	if !ok {
		return nil, nil
	}

	creatorContext := CreatorContextFromIdentity(id)

	return creatorContext, nil
}

func (s *service) FindProfilesByEmail(ctx context.Context, email string) ([]Profile, error) {
	normalized := strings.TrimSpace(email)
	if normalized == "" {
		return []Profile{}, nil
	}

	profiles := make([]Profile, 0)
	for _, issuer := range s.issuers {
		directory := s.directories[issuer]

		identities, err := directory.SearchByEmail(ctx, normalized)
		if err != nil {
			return nil, err
		}
		if len(identities) == 0 {
			continue
		}

		subjects := make([]string, 0, len(identities))
		for _, id := range identities {
			subjects = append(subjects, id.Subject)
		}

		linked, err := s.linkage.UsersForSubjects(ctx, issuer, subjects)
		if err != nil {
			return nil, fmt.Errorf("lookup user ids by subjects: %w", err)
		}

		userIDBySubject := make(map[string]uuid.UUID, len(linked))
		for _, row := range linked {
			userIDBySubject[row.Subject] = row.UserID
		}

		for _, id := range identities {
			userID, ok := userIDBySubject[id.Subject]
			if !ok {
				continue
			}
			profiles = append(profiles, ProfileFromIdentity(userID, id))
		}
	}

	return profiles, nil
}

type DeleteUserHandle interface {
	// Execute removes the external identities (e.g. Ory). It must be called
	// only after the caller has already deleted the database rows.
	Execute(ctx context.Context) error
}

func (s *service) PrepareDeleteUser(ctx context.Context, userID uuid.UUID) (DeleteUserHandle, error) {
	if userID == uuid.Nil {
		return nil, errors.New("user id is required")
	}

	linked, err := s.linkedIdentitiesForUsers(ctx, []uuid.UUID{userID})
	if err != nil {
		return nil, err
	}
	if len(linked) == 0 {
		return nil, fmt.Errorf("%w: no identity mapping for user %s", ErrUserNotFound, userID)
	}

	targets := make([]deleteTarget, 0, len(linked))
	for _, row := range linked {
		directory, err := s.directoryForIssuer(row.Issuer)
		if err != nil {
			return nil, err
		}
		targets = append(targets, deleteTarget{directory: directory, subject: row.Subject})
	}

	return &deleteUserHandle{targets: targets}, nil
}

type deleteTarget struct {
	directory Directory
	subject   string
}

type deleteUserHandle struct {
	targets []deleteTarget
}

func (h *deleteUserHandle) Execute(ctx context.Context) error {
	for _, target := range h.targets {
		if err := target.directory.DeleteIdentity(ctx, target.subject); err != nil {
			return fmt.Errorf("delete identity %s: %w", target.subject, err)
		}
	}

	return nil
}

func (s *service) identitiesByUserID(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Identity, error) {
	unique := uniqueUUIDs(userIDs)
	if len(unique) == 0 {
		return map[uuid.UUID]Identity{}, nil
	}

	linked, err := s.linkedIdentitiesForUsers(ctx, unique)
	if err != nil {
		return nil, err
	}
	if len(linked) == 0 {
		return map[uuid.UUID]Identity{}, nil
	}

	byIssuer := make(map[string][]LinkedIdentity, len(s.issuers))
	for _, row := range linked {
		byIssuer[row.Issuer] = append(byIssuer[row.Issuer], row)
	}

	identities := make(map[uuid.UUID]Identity, len(unique))
	for _, issuer := range s.issuers {
		rows, ok := byIssuer[issuer]
		if !ok {
			continue
		}

		subjects := make([]string, 0, len(rows))
		userIDBySubject := make(map[string]uuid.UUID, len(rows))
		for _, row := range rows {
			subjects = append(subjects, row.Subject)
			userIDBySubject[row.Subject] = row.UserID
		}

		found, err := s.directories[issuer].ListIdentities(ctx, subjects)
		if err != nil {
			return nil, err
		}

		for _, id := range found {
			userID, ok := userIDBySubject[id.Subject]
			if !ok {
				continue
			}
			if _, exists := identities[userID]; exists {
				continue
			}
			identities[userID] = id
		}
	}

	return identities, nil
}

func (s *service) linkedIdentitiesForUsers(ctx context.Context, userIDs []uuid.UUID) ([]LinkedIdentity, error) {
	linked, err := s.linkage.IdentitiesForUsers(ctx, s.issuers, userIDs)
	if err != nil {
		return nil, fmt.Errorf("lookup linked identities: %w", err)
	}

	return linked, nil
}

func uniqueUUIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))

	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	return unique
}

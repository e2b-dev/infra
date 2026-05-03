package teamprovision

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const (
	// supabase uses "email" for password signups; everything else (google,
	// github, apple, gitlab, ...) is treated as a social provider.
	emailAuthProvider = "email"

	signupIPMetadataKey        = "signup_ip"
	signupUserAgentMetadataKey = "signup_user_agent"
	providersMetadataKey       = "providers"
)

// resolveCreatorContext reads signup IP/UA and auth provider from
// auth.users.raw_app_meta_data, which Supabase populates for every signup flow.
// Returns nil when the user cannot be found so callers can keep going without
// the optional context.
func resolveCreatorContext(ctx context.Context, supabaseDB *supabasedb.Client, userID uuid.UUID) (*sharedteamprovision.CreatorContextV1, error) {
	authUser, err := supabaseDB.Write.GetAuthUserByID(ctx, userID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("get auth user: %w", err)
	}

	metadata := map[string]any{}
	if len(authUser.RawAppMetaData) > 0 {
		if err := json.Unmarshal(authUser.RawAppMetaData, &metadata); err != nil {
			return nil, fmt.Errorf("decode raw_app_meta_data: %w", err)
		}
	}

	authMethod := sharedteamprovision.AuthMethodPassword
	if hasOAuthProvider(metadata) {
		authMethod = sharedteamprovision.AuthMethodSocial
	}

	return &sharedteamprovision.CreatorContextV1{
		IPAddress:  stringFromMetadata(metadata, signupIPMetadataKey),
		UserAgent:  stringFromMetadata(metadata, signupUserAgentMetadataKey),
		AuthMethod: authMethod,
	}, nil
}

func hasOAuthProvider(metadata map[string]any) bool {
	rawProviders, ok := metadata[providersMetadataKey].([]any)
	if !ok {
		return false
	}

	for _, p := range rawProviders {
		name, ok := p.(string)
		if ok && name != "" && name != emailAuthProvider {
			return true
		}
	}

	return false
}

func stringFromMetadata(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}

	return ""
}

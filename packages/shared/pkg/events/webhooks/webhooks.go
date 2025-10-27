package webhooks

import (
	"fmt"

	"github.com/google/uuid"
)

const WebhookKeyPrefix = "wh"

func DeriveKey(teamID uuid.UUID) string {
	return fmt.Sprintf("%s:%s", WebhookKeyPrefix, teamID.String())
}

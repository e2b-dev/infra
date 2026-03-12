package internal

import "github.com/google/uuid"

func TryParseUUID(id string) (uuid.UUID, bool) {
	val, err := uuid.Parse(id)

	return val, err == nil && val != uuid.Nil
}

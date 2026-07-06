package database

import (
	"github.com/google/uuid"
)

func ParseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

func UUIDString(id uuid.UUID) string {
	return id.String()
}

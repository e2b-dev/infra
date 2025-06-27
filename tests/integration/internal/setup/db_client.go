package setup

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

func GetTestDBClient() *db.DB {
	database, err := db.NewClient(1, 1)
	if err != nil {
		panic(err)
	}

	return database
}

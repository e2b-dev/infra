package database

import (
	"time"
)

const sqliteDateTimeFormat = "2006-01-02 15:04:05.000000000+00:00"

func (osr *OrchestratorStatusRow) OldestSandboxStartTime() (out time.Time) {
	var err error
	switch tt := osr.EarliestRunningSandboxStartedAt.(type) {
	case string:
		out, err = time.Parse(sqliteDateTimeFormat, tt)
		if err != nil {
			panic(err)
		}

	case time.Time:
		out = tt
	case *time.Time:
		out = *tt
	}
	out = out.UTC()
	return
}

func (osr *OrchestratorStatusRow) MostRecentSandboxModification() (out time.Time) {
	var err error
	switch tt := osr.MostRecentRunningSandboxUpdatedAt.(type) {
	case string:
		out, err = time.Parse(time.DateTime, tt)
		if err != nil {
			panic(err)
		}
	case time.Time:
		out = tt
	case *time.Time:
		out = *tt
	}

	out = out.UTC()

	return
}

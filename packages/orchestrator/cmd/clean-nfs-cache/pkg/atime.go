package pkg

import "time"

type File struct {
	Path         string
	Size         int64
	ATime, BTime time.Time
}

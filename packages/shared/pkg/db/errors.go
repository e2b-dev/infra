package db

type NotFoundError error
type TemplateNotFound struct{ NotFoundError }

func (TemplateNotFound) Error() string {
	return "Template not found"
}

type SnapshotNotFound struct{ NotFoundError }

func (SnapshotNotFound) Error() string {
	return "Snapshot not found"
}

type BuildNotFound struct{ NotFoundError }

func (BuildNotFound) Error() string {
	return "Build not found"
}

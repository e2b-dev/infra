package db

type ErrNotFound error
type TemplateNotFound struct{ ErrNotFound }

func (TemplateNotFound) Error() string {
	return "Template not found"
}

type SnapshotNotFound struct{ ErrNotFound }

func (SnapshotNotFound) Error() string {
	return "Snapshot not found"
}

type BuildNotFound struct{ ErrNotFound }

func (BuildNotFound) Error() string {
	return "Build not found"
}

type EnvNotFound struct{ ErrNotFound }

func (EnvNotFound) Error() string {
	return "Env not found"
}

package db

type NotFoundError error

type TemplateNotFoundError struct{ NotFoundError }

func (TemplateNotFoundError) Error() string {
	return "Template not found"
}

type TemplateBuildNotFoundError struct{ NotFoundError }

func (TemplateBuildNotFoundError) Error() string {
	return "Template build not found"
}

type SnapshotNotFoundError struct{ NotFoundError }

func (SnapshotNotFoundError) Error() string {
	return "Snapshot not found"
}

type BuildNotFoundError struct{ NotFoundError }

func (BuildNotFoundError) Error() string {
	return "Build not found"
}

type EnvNotFoundError struct{ NotFoundError }

func (EnvNotFoundError) Error() string {
	return "Env not found"
}

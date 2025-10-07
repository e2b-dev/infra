package db

type NotFoundError error

type TemplateBuildNotFoundError struct{ NotFoundError }

func (TemplateBuildNotFoundError) Error() string {
	return "Template build not found"
}

type EnvNotFoundError struct{ NotFoundError }

func (EnvNotFoundError) Error() string {
	return "Env not found"
}

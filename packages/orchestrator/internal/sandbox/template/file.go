package template

type File interface {
	Path() string
	Close() error
}

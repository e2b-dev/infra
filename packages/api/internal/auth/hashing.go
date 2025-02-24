package auth

type Hasher interface {
	Hash(key []byte) string
}

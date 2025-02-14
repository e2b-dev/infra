package auth

type Hashing interface {
	Hash(key []byte) string
}

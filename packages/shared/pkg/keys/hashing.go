package keys

type Hasher interface {
	Hash(key []byte) string
}

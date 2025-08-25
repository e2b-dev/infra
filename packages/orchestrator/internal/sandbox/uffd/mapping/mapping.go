package mapping

type Mappings interface {
	GetRange(addr uintptr) (offset int64, pagesize uint64, err error)
}

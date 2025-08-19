package mapping

type Mappings interface {
	GetRange(addr uintptr) (offset uint64, pagesize uint64, err error)
}

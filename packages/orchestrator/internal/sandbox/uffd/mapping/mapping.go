package mapping

type Mappings interface {
	GetRange(addr uintptr) (offset int64, pagesize int64, err error)
}

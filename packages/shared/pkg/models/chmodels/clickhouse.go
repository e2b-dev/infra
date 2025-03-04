package chmodels

type ClickhouseColumn struct {
	Database string `ch:"database"`
	Table    string `ch:"table"`
	Name     string `ch:"name"`
	Type     string `ch:"type"`
	Position uint64 `ch:"position"`
}

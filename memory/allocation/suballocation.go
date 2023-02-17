package allocation

type Suballocation struct {
	Offset   int
	Size     int
	UserData any
	Type     SuballocationType
}

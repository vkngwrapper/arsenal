package defrag

type defragCounterStatus uint32

const (
	defragCounterPass defragCounterStatus = iota
	defragCounterIgnore
	defragCounterEnd
)

var defragCounterStatusMapping = map[defragCounterStatus]string{
	defragCounterPass:   "defragCounterPass",
	defragCounterIgnore: "defragCounterIgnore",
	defragCounterEnd:    "defragCounterEnd",
}

func (s defragCounterStatus) String() string {
	return defragCounterStatusMapping[s]
}

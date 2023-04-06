package defrag

type DefragmentationFlags uint32

const (
	DefragmentationFlagAlgorithmFast DefragmentationFlags = 1 << iota
	DefragmentationFlagAlgorithmBalanced
	DefragmentationFlagAlgorithmFull
	DefragmentationFlagAlgorithmExtensive

	DefragmentationFlagAlgorithmMask = DefragmentationFlagAlgorithmFast |
		DefragmentationFlagAlgorithmBalanced |
		DefragmentationFlagAlgorithmFull |
		DefragmentationFlagAlgorithmExtensive
)

var defragmentationFlagsMapping = map[DefragmentationFlags]string{
	DefragmentationFlagAlgorithmFast:      "DefragmentationFlagAlgorithmFast",
	DefragmentationFlagAlgorithmBalanced:  "DefragmentationFlagAlgorithmBalanced",
	DefragmentationFlagAlgorithmFull:      "DefragmentationFlagAlgorithmFull",
	DefragmentationFlagAlgorithmExtensive: "DefragmentationFlagAlgorithmExtensive",
}

func (f DefragmentationFlags) String() string {
	return defragmentationFlagsMapping[f]
}

type DefragmentationInfo struct {
	Flags DefragmentationFlags

	MaxBytesPerPass       int
	MaxAllocationsPerPass int
}

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

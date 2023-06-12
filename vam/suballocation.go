package vam

type SuballocationType uint32

const (
	SuballocationFree SuballocationType = iota
	SuballocationUnknown
	SuballocationBuffer
	SuballocationImageUnknown
	SuballocationImageLinear
	SuballocationImageOptimal
)

var suballocationTypeMapping = map[SuballocationType]string{
	SuballocationFree:         "SuballocationFree",
	SuballocationUnknown:      "SuballocationUnknown",
	SuballocationBuffer:       "SuballocationBuffer",
	SuballocationImageUnknown: "SuballocationImageUnknown",
	SuballocationImageLinear:  "SuballocationImageLinear",
	SuballocationImageOptimal: "SuballocationImageOptimal",
}

func (s SuballocationType) String() string {
	str, ok := suballocationTypeMapping[s]
	if !ok {
		return "unknown SuballocationType"
	}

	return str
}

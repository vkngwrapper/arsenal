package vam

type suballocationType uint32

const (
	SuballocationFree suballocationType = iota
	SuballocationUnknown
	SuballocationBuffer
	SuballocationImageUnknown
	SuballocationImageLinear
	SuballocationImageOptimal
)

var suballocationTypeMapping = map[suballocationType]string{
	SuballocationFree:         "SuballocationFree",
	SuballocationUnknown:      "SuballocationUnknown",
	SuballocationBuffer:       "SuballocationBuffer",
	SuballocationImageUnknown: "SuballocationImageUnknown",
	SuballocationImageLinear:  "SuballocationImageLinear",
	SuballocationImageOptimal: "SuballocationImageOptimal",
}

func (s suballocationType) String() string {
	str, ok := suballocationTypeMapping[s]
	if !ok {
		return "unknown SuballocationType"
	}

	return str
}

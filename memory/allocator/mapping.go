package allocator

const (
	CounterMinExtraMapping int = 7
)

type mappingHysteresis struct {
	minorCounter int
	majorCounter int
	extraMapping bool
}

func (h *mappingHysteresis) ExtraMapping() bool { return h.extraMapping }

// PostMap is called after Map is called, returns true if extra mapping was activated
// Mapping/unmapping activity will make extramapping more likely to activate by stepping up counters
// when it isn't activated and stepping down counters when it isn't
func (h *mappingHysteresis) PostMap() bool {
	if !h.extraMapping {
		h.majorCounter++
		if h.majorCounter >= CounterMinExtraMapping {
			h.extraMapping = true
			h.majorCounter = 0
			h.minorCounter = 0
			return true
		}
	} else {
		h.postMinorCounter()
	}

	return false
}

// PostUnmap is called after Unmap is called, it allows extra mapping to be stepped up/down
// Mapping/unmapping activity will make extramapping more likely to activate by stepping up counters
// when it isn't activated and stepping down counters when it isn't
func (h *mappingHysteresis) PostUnmap() {
	if !h.extraMapping {
		h.majorCounter++
	} else {
		h.postMinorCounter()
	}
}

// PostAlloc is called after memory is allocated, it allows extra mapping to be stepped up/down
// Alloc/free activity will make extrampping more likely to deactivate by stepping down counters
// when it isn't activated and stepping up counters when it is
func (h *mappingHysteresis) PostAlloc() {
	if h.extraMapping {
		h.majorCounter++
	} else {
		h.postMinorCounter()
	}
}

// PostFree is called after memory is freed, returns true if extra mapping was deactivated
// Alloc/free activity will make extramapping more likely to deactivate by seepping down counters
// when it isn't activated and stepping up counters when it is
func (h *mappingHysteresis) PostFree() bool {
	if h.extraMapping {
		h.majorCounter++
		if h.majorCounter >= CounterMinExtraMapping && h.majorCounter > h.minorCounter+1 {
			h.extraMapping = false
			h.majorCounter = 0
			h.minorCounter = 0
			return true
		}
	} else {
		h.postMinorCounter()
	}

	return false
}

func (h *mappingHysteresis) postMinorCounter() {
	if h.minorCounter < h.majorCounter {
		h.minorCounter++
	} else if h.majorCounter > 0 {
		h.majorCounter--
		h.minorCounter--
	}
}

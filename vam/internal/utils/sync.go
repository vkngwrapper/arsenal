package utils

import (
	"sync"
)

type OptionalMutex struct {
	Mutex    sync.Mutex
	UseMutex bool
}

func (m *OptionalMutex) Lock() {
	if m.UseMutex {
		m.Mutex.Lock()
	}
}

func (m *OptionalMutex) Unlock() {
	if m.UseMutex {
		m.Mutex.Unlock()
	}
}

type OptionalRWMutex struct {
	Mutex    sync.RWMutex
	UseMutex bool
}

func (m *OptionalRWMutex) Lock() {
	if m.UseMutex {
		m.Mutex.Lock()
	}
}

func (m *OptionalRWMutex) Unlock() {
	if m.UseMutex {
		m.Mutex.Unlock()
	}
}

func (m *OptionalRWMutex) RLock() {
	if m.UseMutex {
		m.Mutex.RLock()
	}
}

func (m *OptionalRWMutex) RUnlock() {
	if m.UseMutex {
		m.Mutex.RUnlock()
	}
}

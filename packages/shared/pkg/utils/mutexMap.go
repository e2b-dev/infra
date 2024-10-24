package utils

import "sync"

type KeyMutex struct {
	mu sync.Map // Map of mutexes for each key
}

func (km *KeyMutex) Lock(key string) {
	mutex, _ := km.mu.LoadOrStore(key, &sync.Mutex{})
	mutex.(*sync.Mutex).Lock()
}

func (km *KeyMutex) Unlock(key string) {
	if mutex, ok := km.mu.Load(key); ok {
		mutex.(*sync.Mutex).Unlock()
	}
}

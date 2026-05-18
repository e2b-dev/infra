package utils

import "sync"

// EnvVars distinguishes system-set ("internal") entries from user-provided
// ones; internal entries cannot be touched by the user-facing methods.
type EnvVars struct {
	m sync.Map
}

type envVarEntry struct {
	value    string
	internal bool
}

func NewEnvVars() *EnvVars {
	return &EnvVars{}
}

// Store sets an internal entry, overwriting any existing one.
func (e *EnvVars) Store(key, value string) {
	e.m.Store(key, envVarEntry{value: value, internal: true})
}

// StoreUser sets a user entry; returns false if an internal entry exists.
func (e *EnvVars) StoreUser(key, value string) bool {
	for {
		existing, loaded := e.m.Load(key)
		if loaded && existing.(envVarEntry).internal {
			return false
		}
		newEntry := envVarEntry{value: value}
		if !loaded {
			if _, raced := e.m.LoadOrStore(key, newEntry); !raced {
				return true
			}

			continue
		}
		if e.m.CompareAndSwap(key, existing, newEntry) {
			return true
		}
	}
}

func (e *EnvVars) Load(key string) (string, bool) {
	v, ok := e.m.Load(key)
	if !ok {
		return "", false
	}

	return v.(envVarEntry).value, true
}

func (e *EnvVars) Range(f func(key, value string) bool) {
	e.m.Range(func(k, v any) bool {
		return f(k.(string), v.(envVarEntry).value)
	})
}

// ReplaceUserVars replaces all user entries with newVars; internal entries
// are left untouched even if they appear in newVars.
func (e *EnvVars) ReplaceUserVars(newVars map[string]string) {
	e.m.Range(func(k, v any) bool {
		if v.(envVarEntry).internal {
			return true
		}
		if _, keep := newVars[k.(string)]; !keep {
			e.m.CompareAndDelete(k, v)
		}

		return true
	})
	for k, v := range newVars {
		e.StoreUser(k, v)
	}
}

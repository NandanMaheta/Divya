// Package commonpasswords holds the embedded list of the most common passwords, used by the
// password policy to refuse them.
package commonpasswords

import (
	_ "embed"
	"strings"
	"sync"
)

//go:embed 10k.txt
var raw string

var (
	once sync.Once
	set  map[string]struct{}
)

// Set returns the lowercase common passwords. It is built once; callers must not mutate it.
func Set() map[string]struct{} {
	once.Do(func() {
		lines := strings.Split(raw, "\n")
		set = make(map[string]struct{}, len(lines))
		for _, l := range lines {
			if l != "" {
				set[l] = struct{}{}
			}
		}
	})
	return set
}

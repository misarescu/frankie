package drivers

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ErrUnknownDriver: no driver of that name is registered.
var ErrUnknownDriver = errors.New("unknown release driver")

// Registry holds the release drivers a deployment has, by name. M7's are
// built in (D109); a driver served out of process joins through NewClient.
type Registry struct {
	drivers map[string]Driver
}

// NewRegistry registers ds, each wrapped by Checked. Names must be set and
// unique.
func NewRegistry(ds ...Driver) (*Registry, error) {
	r := &Registry{drivers: make(map[string]Driver, len(ds))}
	for _, d := range ds {
		name := d.Name()
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("drivers: a driver has no name")
		}
		if _, dup := r.drivers[name]; dup {
			return nil, fmt.Errorf("drivers: two drivers named %q", name)
		}
		r.drivers[name] = Checked(d)
	}
	return r, nil
}

// Get returns the driver named name, wrapped by Checked; ErrUnknownDriver
// lists the known ones.
func (r *Registry) Get(name string) (Driver, error) {
	if d, ok := r.drivers[name]; ok {
		return d, nil
	}
	known := "none"
	if names := r.Names(); len(names) > 0 {
		known = strings.Join(names, ", ")
	}
	return nil, fmt.Errorf("%w %q (known: %s)", ErrUnknownDriver, name, known)
}

// Names lists the registered drivers, sorted.
func (r *Registry) Names() []string {
	return slices.Sorted(maps.Keys(r.drivers))
}

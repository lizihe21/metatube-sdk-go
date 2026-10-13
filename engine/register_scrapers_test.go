package engine

import (
	"testing"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

func TestNewMovieProvidersRegistered(t *testing.T) {
	wanted := map[string]bool{"JavLibrary": false, "JavDB": false}
	provider.RangeMovieFactory(func(name string, factory provider.MovieFactory) bool {
		if _, needed := wanted[name]; !needed {
			return true
		}
		instance := factory()
		if instance.Name() != name || instance.Priority() <= 0 {
			t.Errorf("invalid provider registration for %q", name)
		}
		wanted[name] = true
		return true
	})
	for name, found := range wanted {
		if !found {
			t.Errorf("%s not registered", name)
		}
	}
}

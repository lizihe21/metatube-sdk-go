package provider

import (
	"fmt"
	"sync"
)

type (
	MovieFactory = func() MovieProvider
	ActorFactory = func() ActorProvider
)

var (
	// Factory RW Mutex
	factoryMu sync.RWMutex
	// Actor/Movie Factories
	movieFactories = make(map[string]MovieFactory)
	actorFactories = make(map[string]ActorFactory)
)

func Register[T Provider](name string, factory func() T) {
	factoryMu.Lock()
	defer factoryMu.Unlock()

	provider := *new(T)
	registered := false

	if _, ok := any(provider).(ActorProvider); ok {
		actorFactories[name] = func() ActorProvider { return any(factory()).(ActorProvider) }
		registered = true
	}

	if _, ok := any(provider).(MovieProvider); ok {
		movieFactories[name] = func() MovieProvider { return any(factory()).(MovieProvider) }
		registered = true
	}

	if !registered {
		panic(fmt.Sprintf("invalid provider factory: func() %T", provider))
	}
}

func RangeMovieFactory(f func(string, MovieFactory) bool) {
	factoryMu.RLock()
	factories := make(map[string]MovieFactory, len(movieFactories))
	for name, factory := range movieFactories {
		factories[name] = factory
	}
	factoryMu.RUnlock()

	// Invoke external callbacks after releasing the registry lock.
	for name, factory := range factories {
		if !f(name, factory) {
			return
		}
	}
}

func RangeActorFactory(f func(string, ActorFactory) bool) {
	factoryMu.RLock()
	factories := make(map[string]ActorFactory, len(actorFactories))
	for name, factory := range actorFactories {
		factories[name] = factory
	}
	factoryMu.RUnlock()

	// Invoke external callbacks after releasing the registry lock.
	for name, factory := range factories {
		if !f(name, factory) {
			return
		}
	}
}

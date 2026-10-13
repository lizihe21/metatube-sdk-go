package provider

import (
	"testing"
	"time"
)

func assertFactoryWriteLockAvailable(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		factoryMu.Lock()
		// A real registry write proves callbacks released the read lock.
		movieFactories["test-registry-lock-probe"] = nil
		delete(movieFactories, "test-registry-lock-probe")
		factoryMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("provider registry write lock remained held after iteration")
	}
}

func TestRangeMovieFactoryReleasesLockOnEarlyStop(t *testing.T) {
	factoryMu.Lock()
	movieFactories["test-movie-early-stop"] = nil
	factoryMu.Unlock()
	RangeMovieFactory(func(string, MovieFactory) bool { return false })
	assertFactoryWriteLockAvailable(t)
}

func TestRangeActorFactoryReleasesLockOnEarlyStop(t *testing.T) {
	factoryMu.Lock()
	actorFactories["test-actor-early-stop"] = nil
	factoryMu.Unlock()
	RangeActorFactory(func(string, ActorFactory) bool { return false })
	assertFactoryWriteLockAvailable(t)
}

func TestRangeMovieFactoryAllowsCallbackToModifyRegistry(t *testing.T) {
	factoryMu.Lock()
	movieFactories["test-movie-reentrant"] = nil
	factoryMu.Unlock()
	done := make(chan struct{})
	go func() {
		RangeMovieFactory(func(string, MovieFactory) bool {
			factoryMu.Lock()
			movieFactories["test-registry-callback-write"] = nil
			delete(movieFactories, "test-registry-callback-write")
			factoryMu.Unlock()
			return false
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("registry callback executed while read lock was held")
	}
}

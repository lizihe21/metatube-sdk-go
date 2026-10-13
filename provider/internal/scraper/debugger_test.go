package scraper

import (
	"reflect"
	"testing"

	"golang.org/x/text/language"
)

func TestWithLogDebuggerInstallsDebugger(t *testing.T) {
	s := NewScraper("test-debugger", "https://example.test", 1, language.English, WithLogDebugger())
	collector := s.ClonedCollector()
	debuggerField := reflect.ValueOf(collector).Elem().FieldByName("debugger")
	if !debuggerField.IsValid() || debuggerField.IsNil() {
		t.Fatal("WithLogDebugger did not register the Colly debugger")
	}
}

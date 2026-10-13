package googlefree

import (
	"os"
	"testing"
)

func TestGoogleFreeTranslate(t *testing.T) {
	if os.Getenv("METATUBE_LIVE_TEST") != "1" {
		t.Skip("real-network integration test skipped; set METATUBE_LIVE_TEST=1")
	}
	for _, unit := range []struct {
		text, from, to string
	}{
		{"Oh yeah!\nI'm a translator!", "", "zh-CN"},
		{"Oh yeah!\nI'm a translator!", "", "zh-TW"},
		{"Oh yeah!\nI'm a translator!", "", "ja"},
		{"Oh yeah!\nI'm a translator!", "", "de"},
		{"Oh yeah!\nI'm a translator!", "", "fr"},
	} {
		result, err := (&GoogleFree{}).Translate(unit.text, unit.from, unit.to)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(result)
	}
}

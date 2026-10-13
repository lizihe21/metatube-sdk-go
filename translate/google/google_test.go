package google

import (
	"os"
	"testing"
)

func TestGoogleTranslate(t *testing.T) {
	if os.Getenv("METATUBE_LIVE_TEST") != "1" {
		t.Skip("real-network integration test skipped; set METATUBE_LIVE_TEST=1")
	}
	for _, unit := range []struct {
		text, from, to string
	}{
		{`Oh yeah! I'm a translator!`, "", "zh-CN"},
		{`Oh yeah! I'm a translator!`, "", "zh-TW"},
		{`Oh yeah! I'm a translator!`, "", "ja"},
		{`Oh yeah! I'm a translator!`, "", "de"},
		{`Oh yeah! I'm a translator!`, "", "fr"},
	} {
		result, err := (&Google{
			APIKey: os.Getenv("GOOGLE_API_KEY"),
			APIUrl: os.Getenv("GOOGLE_API_URL"),
		}).Translate(unit.text, unit.from, unit.to)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(result)
	}
}

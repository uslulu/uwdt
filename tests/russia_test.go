package backend

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"pwdtt/backend"
)

func TestAggregateNetsSmall(t *testing.T) {
	got := backend.AggregateNets([]string{"10.0.0.0/25", "10.0.0.128/25", "10.0.1.0/24", "10.0.3.0/24", "192.168.0.0/16", "192.168.5.0/24"})
	want := []string{"10.0.0.0/23", "10.0.3.0/24", "192.168.0.0/16"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// Совпадение с эталоном (ipaddress.collapse_addresses) на реальном списке RIPE
func TestAggregateMatchesEmbedded(t *testing.T) {
	raw, err := os.ReadFile("/tmp/ru_raw.txt")
	if err != nil {
		t.Skip("нет сырого списка")
	}
	got := backend.AggregateNets(strings.Split(string(raw), "\n"))
	want := backend.EmbeddedRussiaNets()
	if len(want) < 5000 || !reflect.DeepEqual(got, want) {
		t.Errorf("aggregate %d vs embedded %d", len(got), len(want))
	}
}

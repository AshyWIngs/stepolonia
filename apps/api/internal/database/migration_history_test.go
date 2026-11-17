package database

import "testing"

func TestMigrationHistory(t *testing.T) {
	full := []migrationEvent{{0, true}, {1, true}, {2, true}, {3, true}, {4, true}, {5, true}, {6, true}}
	rolledBack := append(append([]migrationEvent{}, full...), migrationEvent{6, false}, migrationEvent{5, false}, migrationEvent{4, false}, migrationEvent{3, false}, migrationEvent{2, false}, migrationEvent{1, false})
	cases := []struct {
		name    string
		events  []migrationEvent
		version int64
		invalid bool
	}{
		{name: "fresh"},
		{name: "baseline", events: []migrationEvent{{0, true}}},
		{name: "full", events: full, version: 6},
		{name: "rolled back", events: rolledBack},
		{name: "reapply", events: append(append([]migrationEvent{}, rolledBack...), full[1:]...), version: 6},
		{name: "old monolith", events: []migrationEvent{{0, true}, {1, true}}, invalid: true},
		{name: "partial domain set", events: full[:3], invalid: true},
		{name: "previous four-domain set", events: full[:5], invalid: true},
		{name: "gap at max four", events: []migrationEvent{{0, true}, {1, true}, {3, true}, {4, true}}, invalid: true},
		{name: "out of order at max four", events: []migrationEvent{{0, true}, {2, true}, {1, true}, {3, true}, {4, true}}, invalid: true},
		{name: "unknown", events: append(append([]migrationEvent{}, full...), migrationEvent{7, true}), invalid: true},
		{name: "duplicate", events: append(append([]migrationEvent{}, full...), migrationEvent{6, true}), invalid: true},
		{name: "partial down", events: append(append([]migrationEvent{}, full...), migrationEvent{6, false}), invalid: true},
		{name: "out of order down", events: append(append([]migrationEvent{}, full...), migrationEvent{3, false}), invalid: true},
		{name: "repeated baseline", events: []migrationEvent{{0, true}, {0, true}}, invalid: true},
		{name: "negative", events: []migrationEvent{{-1, true}}, invalid: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			version, err := migrationVersion(test.events)
			if (err != nil) != test.invalid {
				t.Fatalf("invalid=%v, error=%v", test.invalid, err)
			}
			if err == nil && version != test.version {
				t.Fatalf("version=%d, want %d", version, test.version)
			}
		})
	}
}

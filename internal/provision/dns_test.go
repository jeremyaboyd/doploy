package provision

import "testing"

func TestZoneForPrefersLongestExistingZone(t *testing.T) {
	zone, record, exists := zoneFor("api.staging.example.com", []string{"example.com", "staging.example.com"}, "")
	if !exists {
		t.Fatal("expected an existing zone to be found")
	}
	if zone != "staging.example.com" || record != "api" {
		t.Errorf("got zone %q record %q, want the delegated child zone to win", zone, record)
	}
}

func TestZoneForApexInExistingZone(t *testing.T) {
	zone, record, exists := zoneFor("example.com", []string{"example.com"}, "")
	if !exists || zone != "example.com" || record != "@" {
		t.Errorf("got zone %q record %q exists %v, want the apex record", zone, record, exists)
	}
}

func TestZoneForDoesNotMatchPartialLabels(t *testing.T) {
	// "ample.com" is a suffix of the string but not of the domain.
	zone, _, exists := zoneFor("example.com", []string{"ample.com"}, "")
	if exists {
		t.Fatalf("matched zone %q across a label boundary", zone)
	}
}

func TestZoneForUsesHintWhenCreating(t *testing.T) {
	zone, record, exists := zoneFor("api.example.co.uk", nil, "example.co.uk")
	if exists {
		t.Fatal("no zones exist, so exists should be false")
	}
	if zone != "example.co.uk" || record != "api" {
		t.Errorf("got zone %q record %q, want the hint to name the zone", zone, record)
	}
}

func TestZoneForFallsBackToLastTwoLabels(t *testing.T) {
	zone, record, exists := zoneFor("api.example.com", nil, "")
	if exists {
		t.Fatal("no zones exist, so exists should be false")
	}
	if zone != "example.com" || record != "api" {
		t.Errorf("got zone %q record %q, want the registrable-domain fallback", zone, record)
	}
}

func TestZoneForIgnoresHintThatDoesNotSuffix(t *testing.T) {
	zone, record, _ := zoneFor("api.other.com", nil, "example.com")
	if zone != "other.com" || record != "api" {
		t.Errorf("got zone %q record %q, want the hint ignored for an unrelated name", zone, record)
	}
}

func TestRecordInAndFQDNOfRoundTrip(t *testing.T) {
	cases := []struct{ zone, fqdn, record string }{
		{"example.com", "example.com", "@"},
		{"example.com", "api.example.com", "api"},
		{"example.com", "a.b.example.com", "a.b"},
	}
	for _, c := range cases {
		if got := recordIn(c.zone, c.fqdn); got != c.record {
			t.Errorf("recordIn(%q, %q) = %q, want %q", c.zone, c.fqdn, got, c.record)
		}
		if got := fqdnOf(c.zone, c.record); got != c.fqdn {
			t.Errorf("fqdnOf(%q, %q) = %q, want %q", c.zone, c.record, got, c.fqdn)
		}
	}
}

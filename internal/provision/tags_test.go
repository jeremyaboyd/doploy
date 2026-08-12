package provision

import (
	"testing"
)

func TestProjectTag(t *testing.T) {
	if got := ProjectTag("demo"); got != "doploy:project:demo" {
		t.Errorf("ProjectTag = %q", got)
	}
}

func TestDropletTagRoundTrips(t *testing.T) {
	tag := DropletTag("demo", "web")

	name, ok := dropletNameFromTags("demo", []string{"unrelated", tag, "another"})
	if !ok {
		t.Fatal("expected to recover the droplet name from its tags")
	}
	if name != "web" {
		t.Errorf("name = %q, want web", name)
	}
}

func TestDropletNameFromTagsIgnoresOtherProjects(t *testing.T) {
	tags := []string{DropletTag("otherproject", "web")}

	if _, ok := dropletNameFromTags("demo", tags); ok {
		t.Error("a droplet from another project must not be claimed")
	}
}

func TestDropletNameFromTagsHandlesNoMatch(t *testing.T) {
	if _, ok := dropletNameFromTags("demo", []string{"web", "production"}); ok {
		t.Error("plain user tags must not be mistaken for doploy ownership tags")
	}
}

func TestTagsForIncludesMarkerAndUserTags(t *testing.T) {
	tags := tagsFor("demo", "web", []string{"production", "frontend"})

	want := map[string]bool{
		MarkerTag:                 false,
		"doploy:project:demo":     false,
		"doploy:droplet:demo:web": false,
		"production":              false,
		"frontend":                false,
	}
	for _, tag := range tags {
		if _, expected := want[tag]; !expected {
			t.Errorf("unexpected tag %q", tag)
			continue
		}
		want[tag] = true
	}
	for tag, seen := range want {
		if !seen {
			t.Errorf("missing tag %q", tag)
		}
	}
}

func TestTagsForDeduplicates(t *testing.T) {
	// A user re-declaring the marker tag should not produce it twice, which the
	// API would reject.
	tags := tagsFor("demo", "web", []string{MarkerTag, "production", "production"})

	counts := map[string]int{}
	for _, tag := range tags {
		counts[tag]++
	}
	for tag, count := range counts {
		if count > 1 {
			t.Errorf("tag %q appears %d times", tag, count)
		}
	}
}

func TestSortedStrings(t *testing.T) {
	got := sortedStrings(map[string]bool{"c": true, "a": true, "b": true})

	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

package pricing

import (
	"math"
	"testing"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/spec"
)

func testSizes() SizeIndex {
	return IndexSizes([]godo.Size{
		{Slug: "s-1vcpu-1gb", Memory: 1024, Vcpus: 1, Disk: 25, PriceMonthly: 6, PriceHourly: 0.00893},
		{Slug: "s-2vcpu-4gb", Memory: 4096, Vcpus: 2, Disk: 80, PriceMonthly: 24, PriceHourly: 0.03571},
	})
}

func boolPtr(v bool) *bool { return &v }

func nearly(a, b float64) bool { return math.Abs(a-b) < 0.0001 }

func TestCalculateSumsDroplets(t *testing.T) {
	s := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web":    {Name: "web", Size: "s-2vcpu-4gb", Region: "nyc3"},
			"worker": {Name: "worker", Size: "s-1vcpu-1gb", Region: "nyc3"},
		},
	}

	est := Calculate(s, testSizes())

	if !nearly(est.TotalMonthly, 30) {
		t.Errorf("monthly total = %v, want 30", est.TotalMonthly)
	}
	if len(est.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", est.Warnings)
	}
}

func TestCalculateIncludesVolumes(t *testing.T) {
	s := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web": {
				Name: "web", Size: "s-1vcpu-1gb", Region: "nyc3",
				Volumes: []*spec.Volume{{Name: "data", SizeGB: 100}},
			},
		},
	}

	est := Calculate(s, testSizes())

	// 6 for the droplet, 100 GiB at $0.10 = 10.
	if !nearly(est.TotalMonthly, 16) {
		t.Errorf("monthly total = %v, want 16", est.TotalMonthly)
	}

	var found bool
	for _, item := range est.Items {
		if item.Kind == KindVolume && item.Name == "data" {
			found = true
			if !nearly(item.Monthly, 10) {
				t.Errorf("volume monthly = %v, want 10", item.Monthly)
			}
		}
	}
	if !found {
		t.Error("volume line item missing")
	}
}

func TestCalculateIncludesBackupsOnlyWhenEnabled(t *testing.T) {
	withBackups := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web": {Name: "web", Size: "s-2vcpu-4gb", Region: "nyc3", Backups: boolPtr(true)},
		},
	}
	// 24 + 20% of 24 = 28.80
	if est := Calculate(withBackups, testSizes()); !nearly(est.TotalMonthly, 28.80) {
		t.Errorf("with backups = %v, want 28.80", est.TotalMonthly)
	}

	withoutBackups := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web": {Name: "web", Size: "s-2vcpu-4gb", Region: "nyc3", Backups: boolPtr(false)},
		},
	}
	if est := Calculate(withoutBackups, testSizes()); !nearly(est.TotalMonthly, 24) {
		t.Errorf("without backups = %v, want 24", est.TotalMonthly)
	}
}

func TestCalculateWarnsOnUnknownSize(t *testing.T) {
	s := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web": {Name: "web", Size: "s-999vcpu-nonsense", Region: "nyc3"},
		},
	}

	est := Calculate(s, testSizes())

	if len(est.Warnings) == 0 {
		t.Fatal("an unpriced size must produce a warning, not a silently low total")
	}
	if est.TotalMonthly != 0 {
		t.Errorf("total = %v, want 0 when nothing could be priced", est.TotalMonthly)
	}
}

func TestCalculateFirewallsAreFree(t *testing.T) {
	s := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web": {
				Name: "web", Size: "s-1vcpu-1gb", Region: "nyc3",
				Firewall: &spec.Firewall{Inbound: []string{"80", "443"}},
			},
		},
	}

	est := Calculate(s, testSizes())
	if !nearly(est.TotalMonthly, 6) {
		t.Errorf("total = %v, want 6; cloud firewalls do not cost anything", est.TotalMonthly)
	}
}

func TestCalculateOrdersItemsByKind(t *testing.T) {
	s := &spec.Spec{
		Project: "demo",
		Droplets: map[string]*spec.Droplet{
			"web": {
				Name: "web", Size: "s-1vcpu-1gb", Region: "nyc3",
				Backups: boolPtr(true),
				Volumes: []*spec.Volume{{Name: "data", SizeGB: 10}},
			},
		},
	}

	est := Calculate(s, testSizes())
	if len(est.Items) != 3 {
		t.Fatalf("expected droplet, volume, and backup items, got %d", len(est.Items))
	}
	wantOrder := []Kind{KindDroplet, KindVolume, KindBackup}
	for i, want := range wantOrder {
		if est.Items[i].Kind != want {
			t.Errorf("item %d kind = %s, want %s", i, est.Items[i].Kind, want)
		}
	}
}

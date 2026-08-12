// Package pricing estimates the monthly cost of a deployment.
//
// Droplet prices come from the live API so they track DigitalOcean's current
// rates. Everything else uses published list prices, declared as constants
// here, because the API does not expose them.
package pricing

import (
	"fmt"
	"sort"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/spec"
)

// Published DigitalOcean list prices, as of the 2025 rate card.
const (
	// VolumePerGiBMonth is block storage, billed per GiB provisioned.
	VolumePerGiBMonth = 0.10

	// BackupWeeklyRate and BackupDailyRate are fractions of the droplet's own
	// monthly price.
	BackupWeeklyRate = 0.20
	BackupDailyRate  = 0.30

	// HoursPerMonth is DigitalOcean's billing month: 30 days. Monthly prices
	// are capped at this many hours.
	HoursPerMonth = 672.0
)

// Kind classifies a line item so output can be grouped.
type Kind string

const (
	KindDroplet  Kind = "droplet"
	KindVolume   Kind = "volume"
	KindBackup   Kind = "backup"
	KindFirewall Kind = "firewall"
)

// LineItem is one billable resource.
type LineItem struct {
	Kind    Kind    `json:"kind"`
	Name    string  `json:"name"`
	Detail  string  `json:"detail"`
	Monthly float64 `json:"monthly"`
	Hourly  float64 `json:"hourly"`
}

// Estimate is the full cost breakdown for a spec.
type Estimate struct {
	Project string     `json:"project"`
	Items   []LineItem `json:"items"`

	TotalMonthly float64 `json:"total_monthly"`
	TotalHourly  float64 `json:"total_hourly"`

	// Warnings records resources that could not be priced, so a missing size
	// slug produces a visible caveat instead of a silently low total.
	Warnings []string `json:"warnings,omitempty"`
}

// SizeIndex maps a size slug to its API record.
type SizeIndex map[string]*godo.Size

// IndexSizes builds a lookup from the API's size list.
func IndexSizes(sizes []godo.Size) SizeIndex {
	index := make(SizeIndex, len(sizes))
	for i := range sizes {
		index[sizes[i].Slug] = &sizes[i]
	}
	return index
}

// Estimate computes the monthly and hourly cost of a spec.
func Calculate(s *spec.Spec, sizes SizeIndex) *Estimate {
	est := &Estimate{Project: s.Project}

	for _, name := range s.DropletNames() {
		d := s.Droplets[name]

		size, known := sizes[d.Size]
		if !known {
			est.Warnings = append(est.Warnings, fmt.Sprintf("droplet %q: unknown size %q, excluded from the total", name, d.Size))
			est.Items = append(est.Items, LineItem{
				Kind:   KindDroplet,
				Name:   name,
				Detail: d.Size + " (unpriced)",
			})
			continue
		}

		monthly := size.PriceMonthly
		hourly := size.PriceHourly
		est.Items = append(est.Items, LineItem{
			Kind:    KindDroplet,
			Name:    name,
			Detail:  fmt.Sprintf("%s / %s / %d vCPU / %d GB RAM / %d GB disk", d.Size, d.Region, size.Vcpus, size.Memory/1024, size.Disk),
			Monthly: monthly,
			Hourly:  hourly,
		})

		if spec.BoolOr(d.Backups, false) {
			est.Items = append(est.Items, LineItem{
				Kind:    KindBackup,
				Name:    name + " backups",
				Detail:  fmt.Sprintf("weekly, %.0f%% of droplet price", BackupWeeklyRate*100),
				Monthly: monthly * BackupWeeklyRate,
				Hourly:  hourly * BackupWeeklyRate,
			})
		}

		for _, v := range d.Volumes {
			volMonthly := float64(v.SizeGB) * VolumePerGiBMonth
			est.Items = append(est.Items, LineItem{
				Kind:    KindVolume,
				Name:    v.Name,
				Detail:  fmt.Sprintf("%d GiB block storage on %s", v.SizeGB, name),
				Monthly: volMonthly,
				Hourly:  volMonthly / HoursPerMonth,
			})
		}

		if d.Firewall != nil {
			est.Items = append(est.Items, LineItem{
				Kind:   KindFirewall,
				Name:   name + " firewall",
				Detail: "cloud firewalls are free",
			})
		}
	}

	for _, item := range est.Items {
		est.TotalMonthly += item.Monthly
		est.TotalHourly += item.Hourly
	}

	// Group the breakdown by kind, then name, so repeated runs read the same.
	sort.SliceStable(est.Items, func(i, j int) bool {
		if est.Items[i].Kind != est.Items[j].Kind {
			return kindOrder(est.Items[i].Kind) < kindOrder(est.Items[j].Kind)
		}
		return est.Items[i].Name < est.Items[j].Name
	})

	return est
}

func kindOrder(k Kind) int {
	switch k {
	case KindDroplet:
		return 0
	case KindVolume:
		return 1
	case KindBackup:
		return 2
	default:
		return 3
	}
}

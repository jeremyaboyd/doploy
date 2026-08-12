package provision

import (
	"context"
	"fmt"
	"time"

	"github.com/digitalocean/godo"
	"github.com/jeremyaboyd/doploy/internal/spec"
	"github.com/jeremyaboyd/doploy/internal/ui"
)

// volumeName namespaces a spec volume name by project, so two projects can both
// declare a volume called "data" without colliding in the account.
func (p *Provisioner) volumeName(name string) string {
	return p.Spec.Project + "-" + name
}

// ensureVolumes creates and attaches every block storage volume in the spec.
//
// Volumes are never resized or deleted here: shrinking is impossible and
// deleting destroys data, so both are left to a deliberate manual action.
func (p *Provisioner) ensureVolumes(ctx context.Context, result *Result) error {
	for _, state := range result.Droplets {
		for _, v := range state.Spec.Volumes {
			if err := p.ensureVolume(ctx, state, v); err != nil {
				return fmt.Errorf("droplet %q volume %q: %w", state.Name, v.Name, err)
			}
		}
	}
	return nil
}

func (p *Provisioner) ensureVolume(ctx context.Context, state *DropletState, v *spec.Volume) error {
	fullName := p.volumeName(v.Name)

	existing, err := p.findVolume(ctx, fullName, state.Spec.Region)
	if err != nil {
		return err
	}

	if existing == nil {
		tags := tagsFor(p.Spec.Project, state.Name, nil)
		if err := ensureTags(ctx, p.Client, tags); err != nil {
			return err
		}

		req := &godo.VolumeCreateRequest{
			Region:          state.Spec.Region,
			Name:            fullName,
			SizeGigaBytes:   int64(v.SizeGB),
			FilesystemType:  filesystemOrDefault(v.FilesystemType),
			FilesystemLabel: v.Name,
			Tags:            tags,
			Description:     fmt.Sprintf("doploy: %s/%s", p.Spec.Project, v.Name),
		}
		ui.Substep("volume %s: creating %d GiB in %s", fullName, v.SizeGB, state.Spec.Region)

		created, _, err := p.Client.Storage.CreateVolume(ctx, req)
		if err != nil {
			return fmt.Errorf("creating volume: %w", err)
		}
		existing = created
	} else {
		if existing.SizeGigaBytes != int64(v.SizeGB) {
			ui.Warn("volume %s: is %d GiB but the spec says %d GiB; doploy does not resize volumes",
				fullName, existing.SizeGigaBytes, v.SizeGB)
		}
		ui.Substep("volume %s: reusing existing volume", fullName)
	}

	return p.attachVolume(ctx, existing, state)
}

// findVolume looks up a volume by exact name within a region.
func (p *Provisioner) findVolume(ctx context.Context, name, region string) (*godo.Volume, error) {
	volumes, _, err := p.Client.Storage.ListVolumes(ctx, &godo.ListVolumeParams{
		Name:        name,
		Region:      region,
		ListOptions: &godo.ListOptions{PerPage: 200},
	})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i], nil
		}
	}
	return nil, nil
}

// attachVolume attaches a volume to a droplet unless it is already attached.
func (p *Provisioner) attachVolume(ctx context.Context, volume *godo.Volume, state *DropletState) error {
	// Scan the whole list before deciding: returning on the first entry would
	// misreport a volume that is attached to our droplet but not listed first.
	for _, id := range volume.DropletIDs {
		if id == state.ID {
			return nil // already attached where it belongs
		}
	}
	if len(volume.DropletIDs) > 0 {
		return fmt.Errorf("volume %s is attached to droplet %d; detach it before deploying (block storage attaches to one droplet at a time)",
			volume.Name, volume.DropletIDs[0])
	}

	ui.Substep("volume %s: attaching to droplet %s", volume.Name, state.Name)
	action, _, err := p.Client.StorageActions.Attach(ctx, volume.ID, state.ID)
	if err != nil {
		return fmt.Errorf("attaching volume: %w", err)
	}
	return p.waitForVolumeAction(ctx, volume.ID, action.ID)
}

// waitForVolumeAction polls a storage action to completion.
func (p *Provisioner) waitForVolumeAction(ctx context.Context, volumeID string, actionID int) error {
	deadline := time.Now().Add(actionTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		action, _, err := p.Client.StorageActions.Get(ctx, volumeID, actionID)
		if err != nil {
			return fmt.Errorf("polling volume action %d: %w", actionID, err)
		}
		switch action.Status {
		case godo.ActionCompleted:
			return nil
		case godo.ActionInProgress:
			// keep waiting
		default:
			return fmt.Errorf("volume action %d failed with status %q", actionID, action.Status)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("volume action %d did not complete within %s", actionID, actionTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func filesystemOrDefault(fs string) string {
	if fs == "" {
		return "ext4"
	}
	return fs
}

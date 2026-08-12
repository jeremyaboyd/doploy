// Package provision reconciles the infrastructure described by a spec against
// what already exists in the DigitalOcean account.
//
// doploy keeps no local state file. Ownership is recorded entirely in resource
// tags, so a deployment can be reconciled from any machine that has a token,
// and losing a laptop never orphans infrastructure.
package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/digitalocean/godo"
)

// Tag prefixes marking resources doploy manages.
const (
	// MarkerTag is applied to everything doploy creates.
	MarkerTag = "doploy"

	projectTagPrefix = "doploy:project:"
	dropletTagPrefix = "doploy:droplet:"
)

// ProjectTag is the tag identifying every resource belonging to a project.
func ProjectTag(project string) string { return projectTagPrefix + project }

// DropletTag identifies one specific droplet within a project. Matching on this
// rather than the droplet's name means a user renaming a droplet in the DO
// console does not cause doploy to build a duplicate.
func DropletTag(project, droplet string) string {
	return dropletTagPrefix + project + ":" + droplet
}

// dropletNameFromTags recovers the spec-level droplet name from a droplet's
// tags. It returns false when the droplet is not one of ours.
func dropletNameFromTags(project string, tags []string) (string, bool) {
	prefix := dropletTagPrefix + project + ":"
	for _, tag := range tags {
		if strings.HasPrefix(tag, prefix) {
			return strings.TrimPrefix(tag, prefix), true
		}
	}
	return "", false
}

// tagsFor builds the full tag set for a droplet: the doploy marker, the project
// and droplet identifiers, and any user-supplied tags.
func tagsFor(project, droplet string, userTags []string) []string {
	tags := []string{MarkerTag, ProjectTag(project), DropletTag(project, droplet)}

	seen := map[string]bool{}
	for _, t := range tags {
		seen[t] = true
	}
	for _, t := range userTags {
		if t != "" && !seen[t] {
			tags = append(tags, t)
			seen[t] = true
		}
	}
	return tags
}

// ensureTags creates tags that do not exist yet. DigitalOcean rejects droplet
// creation referencing an unknown tag, and returns 422 for a tag that already
// exists, so both outcomes are treated as success.
func ensureTags(ctx context.Context, client *godo.Client, tags []string) error {
	for _, name := range tags {
		_, resp, err := client.Tags.Create(ctx, &godo.TagCreateRequest{Name: name})
		if err == nil {
			continue
		}
		if resp != nil && (resp.StatusCode == 422 || resp.StatusCode == 409) {
			continue // already exists
		}
		return fmt.Errorf("creating tag %q: %w", name, err)
	}
	return nil
}

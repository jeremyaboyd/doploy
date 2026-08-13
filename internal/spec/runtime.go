package spec

import (
	"fmt"
	"sort"
	"strings"
)

// RuntimePrefix marks variables whose values cannot exist until droplets do.
//
// A droplet's address is assigned by DigitalOcean at creation time, so a spec
// that wires one service to another host has to name something that is not
// knowable when the file is parsed. These references survive load-time
// interpolation untouched and are substituted by ResolveRuntime once every
// droplet has been provisioned.
const RuntimePrefix = "droplet."

// Runtime field names available on a droplet.
const (
	FieldPrivateIP = "private_ip"
	FieldPublicIP  = "public_ip"
	FieldName      = "name"
)

// IsRuntimeVar reports whether a variable name is resolved after provisioning.
func IsRuntimeVar(name string) bool { return strings.HasPrefix(name, RuntimePrefix) }

// RuntimeVars holds resolved runtime values, keyed by the full variable name
// (for example "droplet.db.private_ip").
type RuntimeVars map[string]string

// Set records one field of one droplet.
func (v RuntimeVars) Set(droplet, field, value string) {
	v[RuntimePrefix+droplet+"."+field] = value
}

// Lookup adapts RuntimeVars to the interpolation interface.
func (v RuntimeVars) Lookup() Lookup {
	return func(name string) (string, bool) {
		value, ok := v[name]
		return value, ok
	}
}

// ResolveString substitutes runtime references in a single string.
//
// Only ${droplet.*} references are touched. Everything else is left exactly as
// written, which matters because this also runs over shell scripts: a setup
// script is full of $VAR and $$ that must survive untouched.
//
// An unresolved reference is an error rather than an empty string. A database
// host that silently becomes "" produces a confusing connection failure much
// later, on the droplet, where it is far harder to diagnose.
func (v RuntimeVars) ResolveString(s string) (string, error) {
	if !strings.Contains(s, "${"+RuntimePrefix) {
		return s, nil
	}
	var firstErr error

	out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		body := match[1:]
		// Bare $NAME and the $$ escape can never be a runtime reference, which
		// needs dots. Leave them alone.
		if !strings.HasPrefix(body, "{") {
			return match
		}
		body = strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")

		name, op, arg := splitVarExpr(body)
		if !IsRuntimeVar(name) {
			return match
		}

		value, ok := v[name]
		if ok {
			return value
		}
		if op == ":-" || op == "-" {
			return arg
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s cannot be resolved (%s)", name, describeBadRef(name, v))
		}
		return ""
	})

	return out, firstErr
}

// ResolveRuntime substitutes runtime references throughout the spec, in place.
// Call it after provisioning, before generating compose files or running setup.
func (s *Spec) ResolveRuntime(vars RuntimeVars) error {
	for _, name := range s.ServiceNames() {
		svc := s.Services[name]

		for _, key := range sortedKeys(svc.Env) {
			resolved, err := vars.ResolveString(svc.Env[key])
			if err != nil {
				return fmt.Errorf("service %q environment %s: %w", name, key, err)
			}
			svc.Env[key] = resolved
		}

		for i, arg := range svc.Command {
			resolved, err := vars.ResolveString(arg)
			if err != nil {
				return fmt.Errorf("service %q command: %w", name, err)
			}
			svc.Command[i] = resolved
		}

		if svc.Build != nil {
			for _, key := range sortedKeys(svc.Build.Args) {
				resolved, err := vars.ResolveString(svc.Build.Args[key])
				if err != nil {
					return fmt.Errorf("service %q build arg %s: %w", name, key, err)
				}
				svc.Build.Args[key] = resolved
			}
		}
	}

	for _, name := range s.DropletNames() {
		setup := s.Droplets[name].Setup
		if setup == nil {
			continue
		}
		for i, cmd := range setup.Run {
			resolved, err := vars.ResolveString(cmd)
			if err != nil {
				return fmt.Errorf("droplet %q setup: %w", name, err)
			}
			setup.Run[i] = resolved
		}
		for _, key := range sortedKeys(setup.Env) {
			resolved, err := vars.ResolveString(setup.Env[key])
			if err != nil {
				return fmt.Errorf("droplet %q setup env %s: %w", name, key, err)
			}
			setup.Env[key] = resolved
		}
	}
	return nil
}

// RuntimeReferences returns every runtime variable the spec mentions, sorted.
// Setup file contents are not included: they are read at deploy time.
func (s *Spec) RuntimeReferences() []string {
	seen := map[string]bool{}

	collect := func(text string) {
		for _, ref := range runtimeRefsIn(text) {
			seen[ref] = true
		}
	}

	for _, name := range s.ServiceNames() {
		svc := s.Services[name]
		for _, key := range sortedKeys(svc.Env) {
			collect(svc.Env[key])
		}
		for _, arg := range svc.Command {
			collect(arg)
		}
		if svc.Build != nil {
			for _, key := range sortedKeys(svc.Build.Args) {
				collect(svc.Build.Args[key])
			}
		}
	}

	for _, name := range s.DropletNames() {
		if setup := s.Droplets[name].Setup; setup != nil {
			for _, cmd := range setup.Run {
				collect(cmd)
			}
			for _, key := range sortedKeys(setup.Env) {
				collect(setup.Env[key])
			}
		}
	}

	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

// runtimeRefsIn extracts the runtime variable names referenced in a string.
func runtimeRefsIn(text string) []string {
	var refs []string
	for _, match := range varPattern.FindAllString(text, -1) {
		body := match[1:]
		if !strings.HasPrefix(body, "{") {
			continue // bare $NAME cannot contain the dots a runtime ref needs
		}
		body = strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")

		name, _, _ := splitVarExpr(body)
		if IsRuntimeVar(name) {
			refs = append(refs, name)
		}
	}
	return refs
}

// ParseRuntimeRef splits "droplet.db.private_ip" into its droplet and field.
func ParseRuntimeRef(ref string) (droplet, field string, ok bool) {
	if !IsRuntimeVar(ref) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, RuntimePrefix)

	droplet, field, found := strings.Cut(rest, ".")
	if !found || droplet == "" || field == "" {
		return "", "", false
	}
	return droplet, field, true
}

// describeBadRef explains why a reference did not resolve, distinguishing an
// unknown droplet from a droplet that simply has no such address.
func describeBadRef(ref string, vars RuntimeVars) string {
	droplet, field, ok := ParseRuntimeRef(ref)
	if !ok {
		return "expected the form ${droplet.NAME.private_ip}"
	}

	for key := range vars {
		if refDroplet, _, ok := ParseRuntimeRef(key); ok && refDroplet == droplet {
			return fmt.Sprintf("droplet %q has no %s; valid fields are %s, %s, and %s",
				droplet, field, FieldPrivateIP, FieldPublicIP, FieldName)
		}
	}
	return fmt.Sprintf("no droplet named %q was provisioned", droplet)
}

// ValidateRuntimeReferences checks that every runtime reference names a droplet
// the spec defines and a field doploy knows how to supply. This runs at load
// time so a typo fails before anything is created.
func (s *Spec) ValidateRuntimeReferences() Errors {
	var errs Errors
	validFields := map[string]bool{FieldPrivateIP: true, FieldPublicIP: true, FieldName: true}

	for _, ref := range s.RuntimeReferences() {
		droplet, field, ok := ParseRuntimeRef(ref)
		if !ok {
			errs = append(errs, fmt.Sprintf("%s is not a valid reference; expected ${droplet.NAME.private_ip}", ref))
			continue
		}
		if _, defined := s.Droplets[droplet]; !defined {
			errs = append(errs, fmt.Sprintf("%s refers to droplet %q, which is not defined", ref, droplet))
		}
		if !validFields[field] {
			errs = append(errs, fmt.Sprintf("%s: unknown field %q; use %s, %s, or %s",
				ref, field, FieldPrivateIP, FieldPublicIP, FieldName))
		}
	}
	return errs
}

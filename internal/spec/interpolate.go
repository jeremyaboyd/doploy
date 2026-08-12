package spec

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// varPattern matches $$ (an escaped dollar), ${...} with optional modifiers,
// and bare $NAME references.
var varPattern = regexp.MustCompile(`\$(\$|\{[^}]*\}|[A-Za-z_][A-Za-z0-9_]*)`)

// Lookup resolves a variable name to a value.
type Lookup func(name string) (string, bool)

// EnvLookup builds a Lookup that consults an explicit map first and falls back
// to the process environment, so a .env file beside the spec can be overridden
// by a real environment variable.
func EnvLookup(overrides map[string]string) Lookup {
	return func(name string) (string, bool) {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		v, ok := overrides[name]
		return v, ok
	}
}

// Interpolate expands variable references in s.
//
// Supported forms, matching docker compose:
//
//	$VAR / ${VAR}      the value, or empty if unset
//	${VAR:-default}    default if unset or empty
//	${VAR-default}     default only if unset
//	${VAR:?message}    error if unset or empty
//	${VAR?message}     error if unset
//	$$                 a literal dollar sign
func Interpolate(s string, lookup Lookup) (string, error) {
	var firstErr error

	out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		body := match[1:]
		if body == "$" {
			return "$"
		}
		if strings.HasPrefix(body, "{") {
			body = strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")
		}

		name, op, arg := splitVarExpr(body)
		if name == "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("malformed variable reference %q", match)
			}
			return ""
		}

		value, found := lookup(name)
		switch op {
		case ":-":
			if !found || value == "" {
				return arg
			}
		case "-":
			if !found {
				return arg
			}
		case ":?":
			if !found || value == "" {
				if firstErr == nil {
					firstErr = varError(name, arg)
				}
				return ""
			}
		case "?":
			if !found {
				if firstErr == nil {
					firstErr = varError(name, arg)
				}
				return ""
			}
		case ":+":
			if found && value != "" {
				return arg
			}
			return ""
		case "+":
			if found {
				return arg
			}
			return ""
		}
		return value
	})

	return out, firstErr
}

func varError(name, message string) error {
	if message == "" {
		return fmt.Errorf("required variable %s is not set", name)
	}
	return fmt.Errorf("required variable %s is not set: %s", name, message)
}

// splitVarExpr breaks "NAME:-default" into its name, operator, and argument.
func splitVarExpr(body string) (name, op, arg string) {
	for _, candidate := range []string{":-", ":?", ":+", "-", "?", "+"} {
		if idx := strings.Index(body, candidate); idx > 0 {
			return body[:idx], candidate, body[idx+len(candidate):]
		}
	}
	return body, "", ""
}

// LoadDotEnv reads a KEY=VALUE file. A missing file is not an error, since a
// .env beside the spec is optional.
func LoadDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	line := 0

	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")

		key, value, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		values[strings.TrimSpace(key)] = unquote(strings.TrimSpace(value))
	}
	return values, scanner.Err()
}

// unquote strips matching surrounding quotes from a .env value.
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// sortedKeys returns a map's keys in sorted order, so every generated artifact
// and printed table has a deterministic ordering.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

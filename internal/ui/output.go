// Package ui renders human-readable tables and status output.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// Table accumulates rows and renders them as aligned, tab-separated columns.
type Table struct {
	headers []string
	rows    [][]string
}

func NewTable(headers ...string) *Table {
	return &Table{headers: headers}
}

// Row appends a row. Values are converted with fmt.Sprint, so callers can pass
// mixed types without stringifying at every call site.
func (t *Table) Row(values ...any) {
	row := make([]string, len(values))
	for i, v := range values {
		row[i] = fmt.Sprint(v)
	}
	t.rows = append(t.rows, row)
}

func (t *Table) Len() int { return len(t.rows) }

// Render writes the table to w. An empty table still prints its headers so the
// caller can tell "no results" apart from "wrong command".
func (t *Table) Render(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	fmt.Fprintln(tw, strings.Join(t.headers, "\t"))
	for _, row := range t.rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// Print renders to stdout.
func (t *Table) Print() { t.Render(os.Stdout) }

// JSON marshals v to stdout with indentation, for --output json.
func JSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Step prints a progress line for a long-running operation.
func Step(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "==> "+format+"\n", args...)
}

// Substep prints an indented detail line beneath a Step.
func Substep(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "    "+format+"\n", args...)
}

// Warn prints a non-fatal warning.
func Warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

// Money formats a dollar amount. Sub-cent values keep enough precision to be
// meaningful for hourly rates.
func Money(v float64) string {
	if v != 0 && v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

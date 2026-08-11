package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/kestra-io/kestra2-flow-migration/internal/input"
	"github.com/kestra-io/kestra2-flow-migration/internal/migrate"
	"github.com/kestra-io/kestra2-flow-migration/internal/output"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	var outDir string
	var check bool
	var stayV1Compatible bool

	root := &cobra.Command{
		Use:     "kestra-migrate [flags] <file.yml|dir>...",
		Short:   "Migrate Kestra flows from v1 to v2",
		Version: fmt.Sprintf("%s (commit %s, built %s)", version, commit, buildDate),
		Long: `Migrate Kestra flow YAML definitions from v1 to v2 format.

Accepts files and/or directories (walked recursively for .yml/.yaml files).
Subdirectory structure is preserved when writing to an output directory.

Use --check to audit flows without modifying them: each flow is printed with
a green tick if already v2-compatible, or a unified diff showing the required
changes. Exits with code 1 if any flows need migration.

Use --stay-v1-compatible to skip migration rules whose output is not valid
on a v1.3 Kestra instance (see migration-documentation/flows-changes.md
"v2-only compatible changes").`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			flows, err := input.Resolve(args)
			if err != nil {
				return err
			}

			var opts []migrate.Option
			if stayV1Compatible {
				opts = append(opts, migrate.StayV1Compatible())
			}

			if check {
				return runCheck(flows, opts)
			}

			w := output.New(outDir, os.Stdout)
			for _, f := range flows {
				migrated, warnings, err := migrate.Apply(f.Content, opts...)
				if err != nil {
					return fmt.Errorf("%s: %w", f.Name, err)
				}
				if err := w.Write(f.Name, migrated); err != nil {
					return err
				}
				for _, warn := range warnings {
					fmt.Fprintf(os.Stderr, "\033[33m⚠  %s: %s\033[0m\n", f.Name, warn)
				}
			}
			return nil
		},
	}

	root.Flags().StringVarP(&outDir, "out", "o", "", "output directory (default: stdout)")
	root.Flags().BoolVar(&check, "check", false, "show migration status for each flow (green tick if v2-compatible, diff if not)")
	root.Flags().BoolVar(&stayV1Compatible, "stay-v1-compatible", false, "skip migration rules whose output is not valid on a v1.3 Kestra instance")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runCheck(flows []input.Flow, opts []migrate.Option) error {
	needsMigration := 0
	for _, f := range flows {
		migrated, warnings, err := migrate.Apply(f.Content, opts...)
		if err != nil {
			fmt.Printf("\033[31m✗ %s: %v\033[0m\n", f.Name, err)
			needsMigration++
			continue
		}
		hasWarnings := len(warnings) > 0
		if bytes.Equal(f.Content, migrated) && !hasWarnings {
			fmt.Printf("\033[32m✔ %s\033[0m\n", f.Name)
			continue
		}
		if bytes.Equal(f.Content, migrated) {
			// Warning-only: no rule rewrote anything, but the flow needs manual
			// work (removed types, pluginDefaults, missing trigger inputs…).
			// Still print the name, otherwise the warnings below are orphaned
			// with no indication of which flow they belong to.
			fmt.Printf("\033[33m⚠ %s\033[0m\n", f.Name)
		} else {
			fmt.Printf("\033[33m✎ %s\033[0m\n", f.Name)
			diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				A:        difflib.SplitLines(string(f.Content)),
				B:        difflib.SplitLines(string(migrated)),
				FromFile: "original",
				ToFile:   "migrated",
				Context:  3,
			})
			fmt.Print(diff)
		}
		for _, warn := range warnings {
			fmt.Printf("\033[31m  ✗ %s\033[0m\n", warn)
		}
		needsMigration++
	}
	fmt.Println()
	if needsMigration > 0 {
		fmt.Printf("\033[1;33m⚠  %d/%d flows need migration\033[0m\n", needsMigration, len(flows))
		os.Exit(1)
	}
	fmt.Printf("\033[1;32m✔  All %d flows are v2-compatible\033[0m\n", len(flows))
	return nil
}

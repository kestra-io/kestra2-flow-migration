package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kestra-io/kestra2-flow-migration/internal/input"
	"github.com/kestra-io/kestra2-flow-migration/internal/migrate"
	"github.com/kestra-io/kestra2-flow-migration/internal/output"
	"github.com/kestra-io/kestra2-flow-migration/internal/update"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// updateCheckTimeout bounds the release lookup. The check runs concurrently
// with the migration, so this is only ever paid when the migration itself
// finishes faster than the network round-trip.
const updateCheckTimeout = 3 * time.Second

func main() {
	// Started before any work so the answer is usually ready by the time the
	// migration ends; the result is printed last, after the flow output.
	notices := startUpdateCheck()

	// runCheck reports "flows need migration" through this rather than
	// os.Exit, so the update notice below is never skipped.
	exitCode := 0

	var outDir string
	var check bool
	var stayV1Compatible bool
	var disableV2Incompatible bool

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
"v2-only compatible changes").

Use --disable-v2-incompatible to keep a bulk migration deployable: flows that
Kestra 2.0 would reject are rewritten into a disabled placeholder labelled
v2-migration: needs-manual-rewrite, with their original definition preserved
as comments.`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			flows, err := input.Resolve(args)
			if err != nil {
				return err
			}

			if stayV1Compatible && disableV2Incompatible {
				return fmt.Errorf("--disable-v2-incompatible describes a v2 deployment and cannot be combined with --stay-v1-compatible")
			}

			var opts []migrate.Option
			if stayV1Compatible {
				opts = append(opts, migrate.StayV1Compatible())
			}
			if disableV2Incompatible {
				opts = append(opts, migrate.DisableV2Incompatible())
			}

			if check {
				return runCheck(flows, opts, &exitCode)
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
					printDocLink(os.Stderr, "   ", warn)
				}
				if disableV2Incompatible && migrate.HasV2Incompatible(warnings) {
					fmt.Fprintf(os.Stderr, "\033[33m→  %s: disabled and labelled %s\033[0m\n", f.Name, "v2-migration: needs-manual-rewrite")
				}
			}
			return nil
		},
	}

	root.Flags().StringVarP(&outDir, "out", "o", "", "output directory (default: stdout)")
	root.Flags().BoolVar(&check, "check", false, "show migration status for each flow (green tick if v2-compatible, diff if not)")
	root.Flags().BoolVar(&stayV1Compatible, "stay-v1-compatible", false, "skip migration rules whose output is not valid on a v1.3 Kestra instance")
	root.Flags().BoolVar(&disableV2Incompatible, "disable-v2-incompatible", false, "rewrite flows Kestra 2.0 would reject into a disabled placeholder labelled v2-migration: needs-manual-rewrite, keeping the original definition as comments")

	err := root.Execute()
	printUpdateNotice(notices)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// startUpdateCheck kicks off the release lookup in the background.
func startUpdateCheck() <-chan *update.Notice {
	notices := make(chan *update.Notice, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		notices <- update.Check(ctx, version)
	}()
	return notices
}

// printUpdateNotice prints the "you are outdated" banner, if any. It waits for
// the lookup only as long as its own timeout allows, and stays silent on every
// failure — an update check must never be the reason a migration looks broken.
func printUpdateNotice(notices <-chan *update.Notice) {
	select {
	case notice := <-notices:
		if notice != nil {
			fmt.Fprintf(os.Stderr, "\n\033[1;33m⚠  %s\033[0m\n", notice)
		}
	case <-time.After(updateCheckTimeout):
	}
}

func runCheck(flows []input.Flow, opts []migrate.Option, exitCode *int) error {
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
			if warn.V2Incompatible {
				fmt.Printf("\033[31m  ✗ %s\033[0m\n", warn)
			} else {
				fmt.Printf("\033[33m  ⚠ %s\033[0m\n", warn)
			}
			printDocLink(os.Stdout, "    ", warn)
		}
		needsMigration++
	}
	fmt.Println()
	if needsMigration > 0 {
		fmt.Printf("\033[1;33m⚠  %d/%d flows need migration\033[0m\n", needsMigration, len(flows))
		*exitCode = 1
		return nil
	}
	fmt.Printf("\033[1;32m✔  All %d flows are v2-compatible\033[0m\n", len(flows))
	return nil
}

// printDocLink prints the official migration-guide page for a warning on its
// own line, so the reader knows where the manual rewrite is documented.
func printDocLink(w io.Writer, indent string, warn migrate.Warning) {
	if warn.DocURL == "" {
		return
	}
	fmt.Fprintf(w, "\033[2m%s↳ docs: %s\033[0m\n", indent, warn.DocURL)
}

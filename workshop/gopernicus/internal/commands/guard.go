package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// runGuard checks a host tree against the host contract (examples/README.md,
// H0–H14, and with --posture the production-posture rules P0–P8 of §3b). It
// prints one status line per rule, any informational lines, one line per
// finding, then a summary. Exit 0 clean, 1 findings, 2 for an invocation or
// input failure. There is deliberately no skip flag, allow file, or config.
func runGuard(args []string) int {
	fs := flag.NewFlagSet("guard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	list := fs.Bool("list", false, "print the canonical sentence of every rule and exit")
	posture := fs.Bool("posture", false, "add the production-posture rules P0–P8 to the run")
	fs.Usage = func() { guardUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	rules := guardRules
	if *posture {
		rules = append(append([]guardRule{}, guardRules...), postureRules...)
	}

	if *list {
		for _, rule := range rules {
			fmt.Printf("%-4s%s\n", rule.id, rule.sentence)
		}
		return 0
	}

	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "gopernicus guard: at most one host root")
		guardUsage(os.Stderr)
		return 2
	}
	root := "."
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus guard: %v\n", err)
		return 2
	}

	host, err := loadHost(resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus guard: %v\n", err)
		return 2
	}
	return reportGuard(os.Stdout, host, rules)
}

// reportGuard evaluates every rule in id order and writes the status lines,
// the informational lines, the findings grouped by rule (each group closed by
// the rule's sentence), and the summary. It returns the exit code.
func reportGuard(w io.Writer, host *guardHost, rules []guardRule) int {
	for _, nested := range host.nested {
		fmt.Fprintf(w, "guard: %s is a nested module — not checked; run guard there\n", nested)
	}

	results := make([]guardResult, len(rules))
	total, failing := 0, 0
	for i, rule := range rules {
		res := rule.eval(host)
		results[i] = res
		unit := res.unit
		if unit == "" {
			unit = "package"
		}
		switch {
		case len(res.findings) > 0:
			total += len(res.findings)
			failing++
			fmt.Fprintf(w, "%-4s %-9s\n", rule.id, plural(len(res.findings), "finding"))
		case res.checked == 0:
			fmt.Fprintf(w, "%-4s %-9s (%s)\n", rule.id, "no scope", res.noScope)
		default:
			fmt.Fprintf(w, "%-4s %-9s (%s)\n", rule.id, "ok", plural(res.checked, unit))
		}
	}

	var notes []guardNote
	for _, res := range results {
		notes = append(notes, res.notes...)
	}
	if len(notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range notes {
			fmt.Fprintf(w, "%-4s %s: %s\n", n.label, n.where, n.msg)
		}
	}

	for i, rule := range rules {
		if len(results[i].findings) == 0 {
			continue
		}
		fmt.Fprintln(w)
		for _, f := range results[i].findings {
			fmt.Fprintf(w, "%-4s%s: %s\n", rule.id, f.where, f.msg)
		}
		fmt.Fprintf(w, "    rule: %s\n", rule.sentence)
	}

	if total > 0 {
		fmt.Fprintf(w, "\nguard: %s across %s\n", plural(total, "finding"), plural(failing, "rule"))
		return 1
	}
	fmt.Fprintf(w, "guard: clean (%d rules, %s)\n", len(rules), plural(len(host.packages), "package"))
	return 0
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func guardUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus guard — check a host against the host contract (H0–H14)

Usage:
  gopernicus guard [host-root]             check the host at host-root (default: .)
  gopernicus guard --posture [host-root]   also check production posture (P0–P8)
  gopernicus guard --list [--posture]      print every rule's canonical sentence

Prints one status line per rule, one line per finding, then a summary. With
--posture it also prints informational lines — ack (an acknowledgement set
true) and rand (a crypto/rand use) — that never change the exit code.
Exit 0 clean, 1 findings, 2 for an invocation or input failure.
There is no --skip, no allow file, and no config.
`)
}

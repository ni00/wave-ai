package bench

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
)

type comparison struct {
	Baseline  string            `json:"baseline_run_id"`
	Candidate string            `json:"candidate_run_id"`
	Passed    bool              `json:"passed"`
	Phases    []phaseComparison `json:"phases"`
}
type phaseComparison struct {
	Workers                   int      `json:"workers"`
	BaselineSamples           int      `json:"baseline_samples"`
	CandidateSamples          int      `json:"candidate_samples"`
	P95ChangePercent          *float64 `json:"p95_change_percent"`
	ModelP95ChangePercent     *float64 `json:"model_p95_change_percent"`
	OutputTokensChangePercent *float64 `json:"output_tokens_change_percent"`
	Reasons                   []string `json:"reasons"`
}

func readReport(path string) (report, error) {
	var r report
	f, e := os.Open(path)
	if e != nil {
		return r, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	if e != nil {
		return r, e
	}
	if len(b) > 128<<20 {
		return r, errors.New("report exceeds 128 MiB")
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.Version != 2 || r.RunID == "" || r.Workload == "" || len(r.Phases) == 0 || (r.Kind != "live" && r.Kind != "synthetic") {
		return r, errors.New("comparison requires Wave version 2 live or synthetic reports")
	}
	return r, nil
}
func change(before, after float64) *float64 {
	if before == 0 {
		if after != 0 {
			return nil
		}
		z := 0.0
		return &z
	}
	n := (after - before) / before * 100
	return &n
}
func compare(args []string, out, diag io.Writer) error {
	fs := flag.NewFlagSet("bench compare", flag.ContinueOnError)
	fs.SetOutput(diag)
	threshold := fs.Float64("max-p95-regression", 10, "maximum allowed end-to-end p95 increase, percent")
	fs.Usage = func() {
		fmt.Fprintln(diag, "Usage: wave bench compare [-max-p95-regression 10] baseline.json candidate.json\nRequires matching workload, load settings and worker phases; failed/incomplete runs cannot pass.")
		fs.PrintDefaults()
	}
	if e := fs.Parse(args); e != nil {
		return e
	}
	if fs.NArg() != 2 || *threshold < 0 || math.IsNaN(*threshold) || math.IsInf(*threshold, 0) {
		return errors.New("bench compare requires two report paths and a finite nonnegative threshold")
	}
	b, e := readReport(fs.Arg(0))
	if e != nil {
		return e
	}
	a, e := readReport(fs.Arg(1))
	if e != nil {
		return e
	}
	bo, ao := b.Options, a.Options
	bo.Workers = ""
	ao.Workers = ""
	if b.Kind != a.Kind || b.Workload != a.Workload || fingerprint(bo) != fingerprint(ao) || len(b.Phases) != len(a.Phases) {
		return errors.New("reports have different workloads, load settings or phase counts")
	}
	r := comparison{Baseline: b.RunID, Candidate: a.RunID, Passed: true, Phases: []phaseComparison{}}
	seen := map[int]bool{}
	for _, bp := range b.Phases {
		var ap *phaseReport
		for i := range a.Phases {
			if a.Phases[i].Workers == bp.Workers {
				if ap != nil {
					return errors.New("duplicate worker phase")
				}
				ap = &a.Phases[i]
			}
		}
		if ap == nil || seen[bp.Workers] {
			return errors.New("reports must have matching unique worker phases")
		}
		seen[bp.Workers] = true
		p := phaseComparison{Workers: bp.Workers, BaselineSamples: bp.Succeeded, CandidateSamples: ap.Succeeded, P95ChangePercent: change(bp.EndToEnd.P95, ap.EndToEnd.P95), ModelP95ChangePercent: change(bp.Model.P95, ap.Model.P95), OutputTokensChangePercent: change(float64(bp.OutputTokens), float64(ap.OutputTokens)), Reasons: []string{}}
		for _, run := range []phaseReport{bp, *ap} {
			if run.Requested < 1 || run.Succeeded != run.Requested || run.Attempted != run.Requested || run.Failed != 0 || run.PhaseError != "" || run.TraceFailures != 0 || run.IncompleteTraces != 0 || run.EndToEnd.Count != run.Succeeded || len(run.Tasks) != run.Attempted {
				p.Reasons = append(p.Reasons, "failed, incomplete or missing telemetry in a compared phase")
				break
			}
		}
		if p.P95ChangePercent == nil || *p.P95ChangePercent > *threshold {
			p.Reasons = append(p.Reasons, "end-to-end p95 regression exceeds threshold")
		}
		if len(p.Reasons) > 0 {
			r.Passed = false
		}
		r.Phases = append(r.Phases, p)
		if bp.Succeeded < 20 || ap.Succeeded < 20 {
			fmt.Fprintln(diag, "bench compare: fewer than 20 successful samples; tail percentiles are exploratory, not statistically conclusive")
		}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if e = enc.Encode(r); e != nil {
		return e
	}
	if !r.Passed {
		return errors.New("benchmark regression or invalid baseline; inspect comparison")
	}
	return nil
}

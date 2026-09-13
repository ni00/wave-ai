package bench

import (
	"math"
	"slices"
	"time"
)

type distribution struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean_ms"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

type phaseReport struct {
	PhaseError     string         `json:"phase_error,omitempty"`
	Workers        int            `json:"workers"`
	Requested      int            `json:"requested"`
	Attempted      int            `json:"attempted"`
	Succeeded      int            `json:"succeeded"`
	Failed         int            `json:"failed"`
	ErrorRate      float64        `json:"error_rate"`
	ElapsedSeconds float64        `json:"elapsed_seconds"`
	TasksPerSecond float64        `json:"successful_tasks_per_second"`
	EndToEnd       distribution   `json:"end_to_end"`
	Queue          distribution   `json:"queue"`
	Execution      distribution   `json:"execution"`
	PeakHeapMiB    float64        `json:"process_peak_sampled_go_heap_mib"`
	DBWaitCount    int64          `json:"db_pool_wait_count"`
	DBWaitMS       float64        `json:"db_pool_wait_ms"`
	States         map[string]int `json:"states"`
	Errors         []string       `json:"error_examples,omitempty"`
}

func (p *phaseReport) summarize(samples []sample, elapsed time.Duration) {
	p.Attempted = len(samples)
	p.ElapsedSeconds = elapsed.Seconds()
	var e2e, queue, execution []float64
	for _, s := range samples {
		p.States[s.state]++
		if s.err != nil {
			p.Failed++
			if len(p.Errors) < 5 {
				p.Errors = append(p.Errors, s.err.Error())
			}
			continue
		}
		p.Succeeded++
		e2e = append(e2e, float64(s.latency)/float64(time.Millisecond))
		queue = append(queue, float64(s.task.StartedAt.Sub(s.task.CreatedAt))/float64(time.Millisecond))
		execution = append(execution, float64(s.task.FinishedAt.Sub(*s.task.StartedAt))/float64(time.Millisecond))
	}
	if p.Attempted > 0 {
		p.ErrorRate = float64(p.Failed) / float64(p.Attempted)
	}
	if elapsed > 0 {
		p.TasksPerSecond = float64(p.Succeeded) / elapsed.Seconds()
	}
	p.EndToEnd, p.Queue, p.Execution = summarize(e2e), summarize(queue), summarize(execution)
}

// Percentiles use nearest rank, including for small sample counts.
func summarize(values []float64) distribution {
	d := distribution{Count: len(values)}
	if len(values) == 0 {
		return d
	}
	slices.Sort(values)
	for _, v := range values {
		d.Mean += v
	}
	d.Mean /= float64(len(values))
	percentile := func(p float64) float64 { return values[int(math.Ceil(p*float64(len(values))))-1] }
	d.P50, d.P95, d.P99, d.Max = percentile(.5), percentile(.95), percentile(.99), values[len(values)-1]
	return d
}

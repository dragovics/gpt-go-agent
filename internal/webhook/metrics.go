package webhook

type metricsProvider interface{ Metrics() map[string]int64 }

func (w *Worker) Metrics() map[string]int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	m := map[string]int64{
		"jobs_total":  int64(len(w.jobs)),
		"queue_depth": int64(len(w.queue)),
	}
	for _, j := range w.jobs {
		m["jobs_"+string(j.Status)]++
		if j.RetryCount > 0 {
			m["jobs_retried"]++
		}
		if j.DeadLettered {
			m["jobs_dead_lettered"]++
		}
		if j.DurationMS > m["job_last_duration_ms"] {
			m["job_last_duration_ms"] = j.DurationMS
		}
	}
	if p, ok := w.gatekeeper.(metricsProvider); ok {
		for k, v := range p.Metrics() {
			m[k] = v
		}
	}
	return m
}

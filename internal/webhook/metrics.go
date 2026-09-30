package webhook

type metricsProvider interface {
	Metrics() map[string]int64
}

func (w *Worker) Metrics() map[string]int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	storageHealthy := int64(0)
	if w.storageHealthy.Load() {
		storageHealthy = 1
	}
	m := map[string]int64{
		"jobs_total":      int64(len(w.jobs)),
		"jobs_capacity":   int64(w.maxJobs),
		"queue_depth":     int64(len(w.queue)),
		"queue_capacity":  int64(cap(w.queue)),
		"storage_healthy": storageHealthy,
		"worker_count":    int64(w.workers),
		"job_timeout_ms":  w.jobTimeout.Milliseconds(),
	}
	for _, j := range w.jobs {
		m["jobs_"+string(j.Status)]++
		if j.RetryCount > 0 {
			m["jobs_retried"]++
		}
		if j.DeadLettered {
			m["jobs_dead_lettered"]++
		}
		if j.DurationMS > m["job_max_duration_ms"] {
			m["job_max_duration_ms"] = j.DurationMS
		}
	}
	if p, ok := w.planner.(metricsProvider); ok {
		for k, v := range p.Metrics() {
			m[k] = v
		}
	}
	return m
}

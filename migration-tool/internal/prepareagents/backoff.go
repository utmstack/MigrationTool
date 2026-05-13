package prepareagents

import "time"

// Backoff schedule (exponential with cap):
//
//	attempt 1 fail -> retry in  30s
//	attempt 2 fail -> retry in  1m
//	attempt 3 fail -> retry in  2m
//	attempt 4 fail -> retry in  4m
//	attempt 5 fail -> retry in  8m
//	attempt 6+     -> capped at 15m (but MaxAttempts caps total retries before this matters)
const (
	BackoffBase    = 30 * time.Second
	BackoffMaxStep = 15 * time.Minute
)

// NextRetryAt returns the time at which an agent that just had its `attempts`-th failure
// should be retried. The schedule is 30s * 2^(attempts-1), capped at BackoffMaxStep.
//
// Caller passes `now` so this function is testable with a clock substitute.
func NextRetryAt(now time.Time, attempts int) time.Time {
	if attempts < 1 {
		attempts = 1
	}

	delay := BackoffBase
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= BackoffMaxStep {
			delay = BackoffMaxStep
			break
		}
	}
	return now.Add(delay)
}

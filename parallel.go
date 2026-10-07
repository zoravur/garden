package garden

import (
	"context"
	"sync"
	"sync/atomic"
)

// parallel runs fn(i) for every i in [0, n) on up to `workers` goroutines.
// It stops handing out work after the first error or when ctx is done, and
// returns that error.
func parallel(ctx context.Context, workers, n int, fn func(i int) error) error {
	workers = min(max(workers, 1), max(n, 1))
	var (
		next     atomic.Int64
		once     sync.Once
		firstErr error
		failed   atomic.Bool
		wg       sync.WaitGroup
	)
	fail := func(err error) {
		once.Do(func() { firstErr = err; failed.Store(true) })
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !failed.Load() {
				if err := ctx.Err(); err != nil {
					fail(err)
					return
				}
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				if err := fn(i); err != nil {
					fail(err)
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

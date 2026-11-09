package main

import (
	"context"
	"log"
)

type udpWakeWorker struct {
	requests chan struct{}
	done     chan struct{}
}

func newUDPWakeWorker(ctx context.Context, w wakeRequester) *udpWakeWorker {
	worker := &udpWakeWorker{requests: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		defer close(worker.done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-worker.requests:
				if ctx.Err() != nil {
					return
				}
				wakeCtx, cancel := context.WithTimeout(ctx, defaultWakePatchTimeout)
				if err := w.RequestWake(wakeCtx); err != nil {
					log.Printf("request UDP wake: %v", err)
				}
				cancel()
			}
		}
	}()
	return worker
}

func (w *udpWakeWorker) request() {
	select {
	case w.requests <- struct{}{}:
	default:
	}
}

// Package parallel decodes the independent blocks of a stream on several goroutines and writes them in stream
// order, as MTCompressor does in the C code (grzip, 4x4).
package parallel

import (
	"fmt"
	"io"
	"sync"

	"github.com/n0madic/go-unarc/internal/method"
)

// Job is one block in flight. The reader fills Header and In; a worker fills Out, or Err. Jobs are recycled:
// In and Out keep their storage from an earlier block.
type Job[H any] struct {
	Header H
	In     []byte
	Out    []byte
	Err    error
	done   chan struct{} // receives one value when Out (or Err) is ready
}

// Read reads the next block of the stream into j. It returns false at the end of the stream. An error also
// ends the stream; it is reported in the place of the block, after the blocks before it are written.
type Read[H any] func(j *Job[H]) (more bool, err error)

// Decode runs read on the calling goroutine and decode on workers goroutines, each with its own state S (zero
// at the start). The decoded blocks are written to dst in stream order. The first error in stream order (a read
// error, a decoding error, a panic as method.ErrBadData, or a dst error such as method.ErrStop)
// ends the output and is returned; everything before it has been written, nothing after it.
func Decode[H, S any](dst io.Writer, workers int, read Read[H], decode func(state *S, j *Job[H])) error {
	workers = max(workers, 1)
	inFlight := workers + workers/2 + 1 // as MTCompressor::CreateJobs

	free := make(chan *Job[H], inFlight)
	for range inFlight {
		free <- &Job[H]{done: make(chan struct{}, 1)}
	}
	work := make(chan *Job[H])
	order := make(chan *Job[H], inFlight)
	stop := make(chan struct{})

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var state S
			for j := range work {
				run(&state, j, decode)
				j.done <- struct{}{}
			}
		}()
	}
	result := make(chan error, 1)
	go func() { result <- write(dst, order, free, stop) }()

	// The queues are closed even if read panics (the caller may recover), so that the workers and the writer end.
	func() {
		defer close(order)
		defer close(work)
		readAll(read, free, work, order, stop)
	}()
	err := <-result
	wg.Wait()
	return err
}

// run decodes one block; a panic (a decoder bug, or data it failed to check) is reported as corrupt data.
func run[H, S any](state *S, j *Job[H], decode func(*S, *Job[H])) {
	defer func() {
		if r := recover(); r != nil {
			j.Err = fmt.Errorf("%w: internal error: %v", method.ErrBadData, r)
		}
	}()
	decode(state, j)
}

// readAll queues the blocks in stream order until the end of the stream, the first read error (queued as a
// failed job, so that the writer reports it in its place) or a stop request from the writer.
func readAll[H any](read Read[H], free, work, order chan *Job[H], stop <-chan struct{}) {
	for {
		var j *Job[H]
		select {
		case j = <-free:
		case <-stop:
			return
		}
		select {
		case <-stop:
			return
		default:
		}
		j.Err, j.Out = nil, j.Out[:0]
		more, err := read(j)
		if err != nil {
			j.Err = err
			j.done <- struct{}{}
			order <- j
			return
		}
		if !more {
			free <- j
			return
		}
		order <- j // never blocks: at most inFlight jobs exist
		work <- j
	}
}

// write writes the decoded blocks in order and returns the first error; after it, the remaining jobs are only
// recycled so that the reader and the workers can finish.
func write[H any](dst io.Writer, order <-chan *Job[H], free chan<- *Job[H], stop chan<- struct{}) error {
	var first error
	for j := range order {
		<-j.done
		if first == nil {
			first = j.Err
			if first == nil && len(j.Out) > 0 {
				n, err := dst.Write(j.Out)
				if err == nil && n < len(j.Out) {
					err = io.ErrShortWrite
				}
				first = err
			}
			if first != nil {
				close(stop)
			}
		}
		free <- j
	}
	return first
}

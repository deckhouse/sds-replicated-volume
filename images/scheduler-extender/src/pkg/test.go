package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	LeaderboardTopSize     = 20
	AdderWorkerNum         = 5
	AdderMaxItemsPerPeriod = 1000
	AdderDelay             = 10 * time.Millisecond
	ClearDelay             = 60 * time.Second
	PrinterDelay           = 100 * time.Millisecond
)

func main() {
	lb := NewLeaderboard()

	// ADDERS
	for range AdderWorkerNum {
		go func() {
			for {
				time.Sleep(AdderDelay)
				// add some random amount of random candidates
				for range rand.Intn(AdderMaxItemsPerPeriod) {
					lb.AddCandidate(randomName())
				}
			}
		}()
	}

	// CLEARER
	go func() {
		for {
			time.Sleep(ClearDelay)
			lb.Clear()
		}
	}()

	// PRINTER
	prev := summary{}
	maxWaited := time.Duration(0)
	immediateReads := 0
	timeoutedReads := 0

	for {
		time.Sleep(PrinterDelay)

		ctx, cancel := context.WithTimeout(context.Background(), PrinterDelay)

		next := lb.ReadOrWait(ctx, prev.Version) // blocks until next update

		if ctx.Err() != nil {
			// timeout
			timeoutedReads++
		} else {
			cancel()
		}

		if next.Waited < 0 {
			immediateReads++
		} else {
			maxWaited = max(maxWaited, next.Waited)
		}

		fmt.Print("\033[H\033[2J") // clear screen
		fmt.Println(time.Now().Format(time.TimeOnly))
		fmt.Printf(
			"ImmediateReads: %d; TimeoutedReads: %d; MaxWaited: %v; LastWait: %v\n",
			immediateReads, timeoutedReads, maxWaited, next.Waited,
		)
		fmt.Print(next.String())

		if next.Lifetime != prev.Lifetime {
			// clear has happened, reset stats
			maxWaited = time.Duration(0)
			immediateReads = 0
			timeoutedReads = 0
		}

		prev = next
	}
}

type leaderboard struct {
	mu   *sync.Mutex
	cond *sync.Cond
	// Why lifetime is needed, why not just zero the version?
	// Simply zeroing the version may be insufficient for
	// [leaderboard.ReadOrWait] to detect reset, because "Signal() does not
	// affect goroutine scheduling priority", and [leaderboard.AddCandidate]
	// may be awoken before a [leaderboard.ReadOrWait] goroutine. It will
	// increment the zeroed version, and [leaderboard.ReadOrWait] may never
	// detect the reset.
	lifetime, version int
	board             []candidate
}

func NewLeaderboard() *leaderboard {
	lb := &leaderboard{}
	lb.mu = &sync.Mutex{}
	lb.cond = sync.NewCond(lb.mu)
	return lb
}

// Reads summary or blocks calling goroutine, if lastVersion is the same as
// current version, i.e. no changes were made since last read.
// Unblocks as soon as any change is made, or context canceled.
// Duration of wait is returned in summary. It will be -1 if there was no wait.
func (s *leaderboard) ReadOrWait(ctx context.Context, lastVersion int) summary {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.version != lastVersion {
		// read immediately
		return s.summary(-1)
	}

	// If ctx canceled at the same time as Wait unblocks,
	// there's a minor chance of a race, when we will be signaling to the next
	// iteration of [leaderboard.ReadOrWait].
	// "awakenerDone" protects against this, because this iteration won't end,
	// until awakener is done.
	awakenerDone := make(chan struct{})
	defer func() {
		<-awakenerDone
	}()

	// childCtx, cancel := context.WithCancel(ctx)
	// defer cancel()

	// "awakener" will awake us, when parent context will be canceled
	go func() {
		<-ctx.Done()
		s.cond.Signal()
		awakenerDone <- struct{}{}
	}()

	// wait
	start := time.Now()
	s.cond.Wait()

	return s.summary(time.Since(start))
}

// Attempts to add candidate's name to the leaderboard.
// If candidate's result is worse then the worst existing candidate,
// there will be no change.
// The probability of the change shrinks with more calls,
// until Clear is called.
func (s *leaderboard) AddCandidate(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	compareCandidates := func(a candidate, b candidate) int {
		return strings.Compare(a.Result, b.Result)
	}

	cand := NewCandidate(name)
	if len(s.board) == LeaderboardTopSize && compareCandidates(cand, s.board[len(s.board)-1]) > 0 {
		// skip this looser
		return
	}

	// signal is required as soon as we know we will be changing state
	defer s.cond.Signal()

	s.board = append(s.board, cand)
	slices.SortStableFunc(s.board, compareCandidates)
	s.board = s.board[:min(LeaderboardTopSize, len(s.board))]
	s.version++
}

func (s *leaderboard) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.cond.Signal()

	s.board = nil
	s.version = 0
	s.lifetime++
}

func (s *leaderboard) summary(waited time.Duration) summary {
	return summary{
		Version:  s.version,
		Lifetime: s.lifetime,
		Waited:   waited,
		Top:      slices.Clone(s.board[0:min(LeaderboardTopSize, len(s.board))]),
	}
}

type candidate struct {
	Name   string
	Result string
}

func NewCandidate(name string) candidate {
	hash := md5.Sum([]byte(name))
	return candidate{
		Name:   name,
		Result: hex.EncodeToString(hash[:]),
	}
}

type summary struct {
	Lifetime, Version int
	Waited            time.Duration
	Top               []candidate
}

func (s summary) String() string {
	sb := &strings.Builder{}
	sb.WriteString(fmt.Sprintf("Lifetime: %d; Version: %d\n\n", s.Lifetime, s.Version))
	for i, c := range s.Top {
		sb.WriteString(fmt.Sprintf("\t%d) %s <= %s\n", i+1, c.Result, c.Name))
	}

	return sb.String()
}

func randomName() string {
	nameLen := 2 + rand.Intn(14)
	name := make([]byte, nameLen)
	name[0] = byte(rand.Intn('Z'-'A')) + 'A'
	for i := 1; i < nameLen; i++ {
		name[i] = byte(rand.Intn('z'-'a')) + 'a'
	}
	return string(name)
}

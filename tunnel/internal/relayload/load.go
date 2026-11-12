// Package relayload measures an open-loop UDP echo workload.
package relayload

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Profile is offered independently to each endpoint.
type Profile struct {
	Clients int
	Hz      int
	Bytes   int
	Seconds int
}

// Result reports actual sends and unique replies within the one-second drain.
// Percentiles are milliseconds; empty samples encode as null.
type Result struct {
	Endpoint                                     string
	Profile                                      Profile
	Scheduled, Sent, Received                    int
	LateTicks                                    int
	SentPerSecond, LossPercent                   float64
	RTTP95ms, RTTP99ms, JitterP95ms, JitterP99ms *float64
}

func (p Profile) validate() error {
	if p.Clients < 1 || p.Clients > 512 || p.Hz < 1 || p.Hz > 1000 || p.Bytes < 16 || p.Bytes > 1400 || p.Seconds < 1 || p.Seconds > 300 || p.Clients*p.Hz*p.Seconds > 2_000_000 {
		return errors.New("need 1-512 clients, 1-1000 Hz, 16-1400 bytes, 1-300 seconds, at most 2 million samples")
	}
	return nil
}

type clientResult struct {
	sent, late int
	rtt        []time.Duration
}

// Measure allocates a connected UDP socket per client, spaces sends across
// each tick and receives concurrently. Missed ticks are counted and skipped
// so a saturated load generator cannot fake the requested rate with bursts.
func Measure(ctx context.Context, endpoint string, p Profile) (Result, error) {
	result := Result{Endpoint: endpoint, Profile: p}
	if err := p.validate(); err != nil {
		return result, err
	}
	target, err := net.ResolveUDPAddr("udp", endpoint)
	if err != nil {
		return result, fmt.Errorf("resolve target: %w", err)
	}
	conns := make([]*net.UDPConn, 0, p.Clients)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for range p.Clients {
		conn, err := net.DialUDP("udp", nil, target)
		if err != nil {
			return result, fmt.Errorf("open client: %w", err)
		}
		conns = append(conns, conn)
	}
	ids := make([]byte, 8*p.Clients)
	if _, err := rand.Read(ids); err != nil {
		return result, fmt.Errorf("generate run ID: %w", err)
	}
	start := time.Now().Add(100 * time.Millisecond)
	clients := make([]clientResult, p.Clients)
	var workers sync.WaitGroup
	for i, conn := range conns {
		workers.Go(func() { clients[i] = drive(ctx, conn, p, start, [8]byte(ids[8*i:8*i+8]), i) })
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Scheduled = p.Clients * p.Hz * p.Seconds
	var rtts, jitter []float64
	for _, c := range clients {
		result.Sent += c.sent
		result.LateTicks += c.late
		var prev time.Duration
		for _, rtt := range c.rtt {
			if rtt > 0 {
				rtts = append(rtts, float64(rtt)/float64(time.Millisecond))
				if prev > 0 {
					jitter = append(jitter, math.Abs(float64(rtt-prev))/float64(time.Millisecond))
				}
			}
			prev = rtt
		}
	}
	result.Received = len(rtts)
	if result.Sent == 0 {
		return result, errors.New("no packets sent")
	}
	result.SentPerSecond = float64(result.Sent) / float64(p.Seconds)
	result.LossPercent = 100 * float64(result.Sent-result.Received) / float64(result.Sent)
	result.RTTP95ms, result.RTTP99ms = percentiles(rtts)
	result.JitterP95ms, result.JitterP99ms = percentiles(jitter)
	return result, nil
}

func percentiles(samples []float64) (*float64, *float64) {
	if len(samples) == 0 {
		return nil, nil
	}
	slices.Sort(samples)
	return &samples[(95*len(samples)+99)/100-1], &samples[(99*len(samples)+99)/100-1]
}

func drive(ctx context.Context, c *net.UDPConn, p Profile, start time.Time, id [8]byte, client int) clientResult {
	n := p.Hz * p.Seconds
	result := clientResult{rtt: make([]time.Duration, n)}
	period := time.Second / time.Duration(p.Hz)
	offset := time.Duration(client) * period / time.Duration(p.Clients)
	_ = c.SetDeadline(start.Add(time.Duration(p.Seconds)*time.Second + time.Second))
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	sentAt := make([]atomic.Int64, n)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, p.Bytes+1)
		for {
			size, err := c.Read(buf)
			if err != nil {
				var timeout net.Error
				if (errors.As(err, &timeout) && timeout.Timeout()) || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				// Connected UDP receives asynchronous ICMP errors. Remaining
				// samples can still succeed if the path recovers.
				continue
			}
			if size != p.Bytes || [8]byte(buf[:8]) != id {
				continue
			}
			seq := binary.BigEndian.Uint32(buf[8:12])
			if int64(seq) >= int64(n) || result.rtt[seq] != 0 {
				continue
			}
			sent := time.Duration(sentAt[seq].Load())
			elapsed := time.Since(start)
			if sent > 0 && sent < elapsed {
				result.rtt[seq] = elapsed - sent
			}
		}
	}()
	packet := make([]byte, p.Bytes)
	copy(packet, id[:])
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for seq := uint32(0); int64(seq) < int64(n); seq++ {
		due := start.Add(offset + time.Duration(seq)*period)
		timer.Reset(time.Until(due))
		select {
		case <-ctx.Done():
			<-done
			return result
		case <-timer.C:
		}
		now := time.Now()
		if now.Sub(due) >= period {
			result.late++
			continue
		}
		binary.BigEndian.PutUint32(packet[8:12], seq)
		sentAt[seq].Store(int64(now.Sub(start)))
		if _, err := c.Write(packet); err == nil {
			result.sent++
		}
	}
	<-done
	return result
}

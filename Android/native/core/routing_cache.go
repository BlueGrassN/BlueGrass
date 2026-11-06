package bridge

import (
	"context"
	"net/netip"
	"time"

	mdns "github.com/miekg/dns"
)

// All fields are protected by fakeNames.mu. A flight belongs to the session,
// not its first caller. Last-waiter cancellation removes it before canceling
// I/O so a fresh caller never joins an abandoned operation.
type routingFlight struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	answer  routingAnswer
	err     error
}

func (r *dnsRouter) lookupRouting(ctx context.Context, host string, target dnsExchange) ([]netip.Addr, error) {
	if ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return nil, errDNS
	}
	if err := r.fake.reserve(host); err != nil {
		return nil, err
	}
	f := &r.fake
	f.mu.Lock()
	if f.closed || ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		f.mu.Unlock()
		return nil, errDNS
	}
	if a, ok := f.answers[host]; ok {
		if time.Now().Before(a.expires) {
			addresses := append([]netip.Addr(nil), a.addresses...)
			f.mu.Unlock()
			return addresses, nil
		}
		delete(f.answers, host)
	}
	flight := f.flights[host]
	if flight == nil {
		call, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		flight = &routingFlight{ctx: call, cancel: cancel, done: make(chan struct{})}
		if f.flights == nil {
			f.flights = make(map[string]*routingFlight)
		}
		f.flights[host] = flight
		go r.runRoutingFlight(host, target, flight)
	}
	flight.waiters++
	f.mu.Unlock()
	select {
	case <-flight.done:
	case <-ctx.Done():
	case <-r.ctx.Done():
	}
	f.mu.Lock()
	flight.waiters--
	if flight.waiters == 0 && f.flights[host] == flight {
		f.stopFlightLocked(host)
	}
	a, err := flight.answer, flight.err
	closed := f.closed
	f.mu.Unlock()
	if err != nil || closed || ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() || len(a.addresses) == 0 {
		return nil, errDNS
	}
	return append([]netip.Addr(nil), a.addresses...), nil
}

func (r *dnsRouter) runRoutingFlight(host string, target dnsExchange, flight *routingFlight) {
	defer flight.cancel()
	a, err := r.fetchRoutingAnswer(flight.ctx, host, target)
	f := &r.fake
	f.mu.Lock()
	defer f.mu.Unlock()
	// A seed, close or abandoned caller set may have replaced this flight.
	// Its late result must never resurrect or overwrite another generation.
	if f.flights[host] != flight {
		return
	}
	delete(f.flights, host)
	if f.closed || flight.ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		err = errDNS
	}
	if err == nil {
		f.storeAnswerLocked(host, a)
	}
	flight.answer, flight.err = a, err
	close(flight.done)
}

func (r *dnsRouter) fetchRoutingAnswer(ctx context.Context, host string, target dnsExchange) (routingAnswer, error) {
	if target == nil || ctx.Err() != nil || !r.platform.valid() {
		return routingAnswer{}, errDNS
	}
	m := new(mdns.Msg).SetQuestion(mdns.Fqdn(host), mdns.TypeA)
	answer, err := target.Exchange(ctx, m)
	if err != nil || ctx.Err() != nil || r.ctx.Err() != nil || !r.platform.valid() {
		return routingAnswer{}, errDNS
	}
	return validatedRoutingAnswer(m, answer)
}

func (f *fakeNames) stopFlightLocked(host string) {
	if flight := f.flights[host]; flight != nil {
		delete(f.flights, host)
		flight.err = errDNS
		close(flight.done)
		flight.cancel()
	}
}

func (f *fakeNames) seed(host string, a routingAnswer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byName[host]; !ok || f.closed {
		return errDNS
	}
	f.storeAnswerLocked(host, a)
	if flight := f.flights[host]; flight != nil {
		delete(f.flights, host)
		flight.answer = a
		if len(a.addresses) == 0 {
			flight.err = errDNS
		}
		close(flight.done)
		flight.cancel()
	}
	return nil
}

func (f *fakeNames) storeAnswerLocked(host string, a routingAnswer) {
	delete(f.answers, host)
	if len(a.addresses) != 0 && a.ttl > 0 && time.Now().Before(a.expires) {
		if f.answers == nil {
			f.answers = make(map[string]routingAnswer)
		}
		a.addresses = append([]netip.Addr(nil), a.addresses...)
		f.answers[host] = a
	}
}

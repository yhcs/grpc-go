/*
 *
 * Copyright 2020 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Package testutils provides utility types, for use in xds tests.
package testutils

import (
	"fmt"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

// TestSubConnsCount is the number of TestSubConns initialized as part of
// package init.
const TestSubConnsCount = 16

// testingLogger wraps the logging methods from testing.T.
type testingLogger interface {
	Log(args ...interface{})
	Logf(format string, args ...interface{})
}

// TestSubConns contains a list of SubConns to be used in tests.
var TestSubConns []*TestSubConn

func init() {
	for i := 0; i < TestSubConnsCount; i++ {
		TestSubConns = append(TestSubConns, &TestSubConn{
			id: fmt.Sprintf("sc%d", i),
		})
	}
}

// TestSubConn implements the SubConn interface, to be used in tests.
type TestSubConn struct {
	id string
}

// UpdateAddresses panics.
func (tsc *TestSubConn) UpdateAddresses([]resolver.Address) { panic("not implemented") }

// Connect is a no-op.
func (tsc *TestSubConn) Connect() {}

// String implements stringer to print human friendly error message.
func (tsc *TestSubConn) String() string {
	return tsc.id
}

// TestClientConn is a mock balancer.ClientConn used in tests.
type TestClientConn struct {
	logger testingLogger

	NewSubConnAddrsCh chan []resolver.Address // the last 10 []Address to create subconn.
	NewSubConnCh      chan balancer.SubConn   // the last 10 subconn created.
	RemoveSubConnCh   chan balancer.SubConn   // the last 10 subconn removed.

	NewPickerCh chan balancer.Picker    // the last picker updated.
	NewStateCh  chan connectivity.State // the last state.

	subConnIdx int
}

// NewTestClientConn creates a TestClientConn.
func NewTestClientConn(t *testing.T) *TestClientConn {
	return &TestClientConn{
		logger: t,

		NewSubConnAddrsCh: make(chan []resolver.Address, 10),
		NewSubConnCh:      make(chan balancer.SubConn, 10),
		RemoveSubConnCh:   make(chan balancer.SubConn, 10),

		NewPickerCh: make(chan balancer.Picker, 1),
		NewStateCh:  make(chan connectivity.State, 1),
	}
}

// NewSubConn creates a new SubConn.
func (tcc *TestClientConn) NewSubConn(a []resolver.Address, o balancer.NewSubConnOptions) (balancer.SubConn, error) {
	sc := TestSubConns[tcc.subConnIdx]
	tcc.subConnIdx++

	tcc.logger.Logf("testClientConn: NewSubConn(%v, %+v) => %s", a, o, sc)
	select {
	case tcc.NewSubConnAddrsCh <- a:
	default:
	}

	select {
	case tcc.NewSubConnCh <- sc:
	default:
	}

	return sc, nil
}

// RemoveSubConn removes the SubConn.
func (tcc *TestClientConn) RemoveSubConn(sc balancer.SubConn) {
	tcc.logger.Logf("testClientCOnn: RemoveSubConn(%p)", sc)
	select {
	case tcc.RemoveSubConnCh <- sc:
	default:
	}
}

// UpdateBalancerState implements balancer.Balancer API. It will be removed when
// switching to the new balancer interface.
func (tcc *TestClientConn) UpdateBalancerState(s connectivity.State, p balancer.Picker) {
	panic("not implemented")
}

// UpdateState updates connectivity state and picker.
func (tcc *TestClientConn) UpdateState(bs balancer.State) {
	tcc.logger.Logf("testClientConn: UpdateState(%v)", bs)
	select {
	case <-tcc.NewStateCh:
	default:
	}
	tcc.NewStateCh <- bs.ConnectivityState

	select {
	case <-tcc.NewPickerCh:
	default:
	}
	tcc.NewPickerCh <- bs.Picker
}

// ResolveNow panics.
func (tcc *TestClientConn) ResolveNow(resolver.ResolveNowOptions) {
	panic("not implemented")
}

// Target panics.
func (tcc *TestClientConn) Target() string {
	panic("not implemented")
}

// IsRoundRobin checks whether f's return value is roundrobin of elements from
// want. But it doesn't check for the order. Note that want can contain
// duplicate items, which makes it weight-round-robin.
//
// Step 1. the return values of f should form a permutation of all elements in
// want, but not necessary in the same order. E.g. if want is {a,a,b}, the check
// fails if f returns:
//  - {a,a,a}: third a is returned before b
//  - {a,b,b}: second b is returned before the second a
//
// If error is found in this step, the returned error contains only the first
// iteration until where it goes wrong.
//
// Step 2. the return values of f should be repetitions of the same permutation.
// E.g. if want is {a,a,b}, the check failes if f returns:
//  - {a,b,a,b,a,a}: though it satisfies step 1, the second iteration is not
//  repeating the first iteration.
//
// If error is found in this step, the returned error contains the first
// iteration + the second iteration until where it goes wrong.
func IsRoundRobin(want []balancer.SubConn, f func() balancer.SubConn) error {
	wantSet := make(map[balancer.SubConn]int) // SubConn -> count, for weighted RR.
	for _, sc := range want {
		wantSet[sc]++
	}

	// The first iteration: makes sure f's return values form a permutation of
	// elements in want.
	//
	// Also keep the returns values in a slice, so we can compare the order in
	// the second iteration.
	gotSliceFirstIteration := make([]balancer.SubConn, 0, len(want))
	for range want {
		got := f()
		gotSliceFirstIteration = append(gotSliceFirstIteration, got)
		wantSet[got]--
		if wantSet[got] < 0 {
			return fmt.Errorf("non-roundrobin want: %v, result: %v", want, gotSliceFirstIteration)
		}
	}

	// The second iteration should repeat the first iteration.
	var gotSliceSecondIteration []balancer.SubConn
	for i := 0; i < 2; i++ {
		for _, w := range gotSliceFirstIteration {
			g := f()
			gotSliceSecondIteration = append(gotSliceSecondIteration, g)
			if w != g {
				return fmt.Errorf("non-roundrobin, first iter: %v, second iter: %v", gotSliceFirstIteration, gotSliceSecondIteration)
			}
		}
	}

	return nil
}

// testClosure is a test util for TestIsRoundRobin.
type testClosure struct {
	r []balancer.SubConn
	i int
}

func (tc *testClosure) next() balancer.SubConn {
	ret := tc.r[tc.i]
	tc.i = (tc.i + 1) % len(tc.r)
	return ret
}

func init() {
	balancer.Register(&TestConstBalancerBuilder{})
}

// ErrTestConstPicker is error returned by test const picker.
var ErrTestConstPicker = fmt.Errorf("const picker error")

// TestConstBalancerBuilder is a balancer builder for tests.
type TestConstBalancerBuilder struct{}

// Build builds a test const balancer.
func (*TestConstBalancerBuilder) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return &testConstBalancer{cc: cc}
}

// Name returns test-const-balancer name.
func (*TestConstBalancerBuilder) Name() string {
	return "test-const-balancer"
}

type testConstBalancer struct {
	cc balancer.ClientConn
}

func (tb *testConstBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	tb.cc.UpdateState(balancer.State{ConnectivityState: connectivity.Ready, Picker: &TestConstPicker{Err: ErrTestConstPicker}})
}

func (tb *testConstBalancer) ResolverError(error) {
	panic("not implemented")
}

func (tb *testConstBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
	if len(s.ResolverState.Addresses) == 0 {
		return nil
	}
	tb.cc.NewSubConn(s.ResolverState.Addresses, balancer.NewSubConnOptions{})
	return nil
}

func (*testConstBalancer) Close() {
}

// TestConstPicker is a const picker for tests.
type TestConstPicker struct {
	Err error
	SC  balancer.SubConn
}

// Pick returns the const SubConn or the error.
func (tcp *TestConstPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if tcp.Err != nil {
		return balancer.PickResult{}, tcp.Err
	}
	return balancer.PickResult{SubConn: tcp.SC}, nil
}

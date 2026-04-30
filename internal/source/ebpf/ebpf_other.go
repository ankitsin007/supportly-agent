//go:build !linux

package ebpf

import (
	"context"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// stubImpl is the non-Linux ebpfImpl. start() always returns
// ErrUnsupported. main.go logs that as a warning and continues with
// the other sources rather than crashing the whole agent.
type stubImpl struct{}

func (stubImpl) start(s *Source, _ chan<- source.RawLog) error {
	s.recordErr(ErrUnsupported)
	return ErrUnsupported
}

func (stubImpl) stop() error { return nil }

// Start on non-Linux: short-circuit with the unsupported error.
// ctx is intentionally unused; Linux's Start uses it to cancel the
// uprobe reader goroutines.
func (s *Source) Start(_ context.Context, out chan<- source.RawLog) error {
	s.impl = stubImpl{}
	return s.impl.start(s, out)
}

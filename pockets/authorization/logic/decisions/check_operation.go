package decisions

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

func (s *Service) withOperation(ctx context.Context, evaluate func(context.Context, *Service) error) error {
	if s == nil {
		return fmt.Errorf("authorization: nil decision service: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var diagnostics *operationDiagnostics
	run := func(ctx context.Context, reader tuples.Reader) error {
		if isNilReader(reader) {
			return fmt.Errorf("authorization: nil snapshot reader: %w", sdk.ErrInvalidInput)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		view := s.bound(reader)
		if s.diagnostic != nil {
			diagnostics = &operationDiagnostics{}
			view.diagnostics = diagnostics
		}
		err := evaluate(ctx, view)
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return err
	}
	var err error
	if s.inSnapshot {
		err = evaluate(ctx, s)
	} else if s.tupleCache != nil {
		err = s.tupleCache.Run(ctx, run)
	} else if snapshots, ok := s.store.(tuples.Snapshotter); ok {
		err = snapshots.ReadTupleSnapshot(ctx, run)
	} else {
		return fmt.Errorf("authorization: coherent tuple snapshot required: %w", sdk.ErrInvalidInput)
	}
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err == nil && !s.inSnapshot && diagnostics != nil && diagnostics.globalGrant {
		s.diagnostic(ctx, DiagnosticGlobalGrantNotApplied)
	}
	return err
}

type memoFacts struct {
	tuples.Reader
	values map[tuples.Tuple]bool
}

func (m *memoFacts) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if v, ok := m.values[t]; ok {
		return v, nil
	}
	v, err := m.Reader.Contains(ctx, t)
	if err == nil {
		m.values[t] = v
	}
	return v, err
}

func (m *memoFacts) ContainsMany(ctx context.Context, ts []tuples.Tuple) ([]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]bool, len(ts))
	missing := []tuples.Tuple{}
	positions := []int{}
	for i, t := range ts {
		if v, ok := m.values[t]; ok {
			out[i] = v
		} else {
			missing = append(missing, t)
			positions = append(positions, i)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	values, err := m.Reader.ContainsMany(ctx, missing)
	if err != nil {
		return nil, err
	}
	if len(values) != len(missing) {
		return nil, fmt.Errorf("invalid exact batch cardinality: %w", sdk.ErrUnavailable)
	}
	for i, v := range values {
		out[positions[i]] = v
		m.values[missing[i]] = v
	}
	return out, nil
}

package decisions

import (
	"context"
	"fmt"
	"iter"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// batchRead is one missing fact, yielded by an ordinary Check. A targets read
// uses only resourceType, resourceID and relation in key. The scheduler fills
// the response before resuming the check; no datastore runs inside an iterator.
type batchRead struct {
	tuple   *tuples.Tuple
	key     directKey
	direct  bool
	allowed bool
	targets []RelationTarget
	err     error
}

type yieldingReader struct{ yield func(*batchRead) bool }

func (r yieldingReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, rid, rel, st, sid string, limit int) (bool, error) {
	read := &batchRead{direct: true, key: directKey{rt, rid, rel, st, sid, limit}}
	if !r.yield(read) {
		return false, context.Canceled // stop unwinds the check without another yield
	}
	return read.allowed, read.err
}

func (r yieldingReader) GetRelationTargets(ctx context.Context, rt, rid, rel string) ([]RelationTarget, error) {
	read := &batchRead{key: directKey{resourceType: rt, resourceID: rid, relation: rel}}
	if !r.yield(read) {
		return nil, context.Canceled
	}
	return read.targets, read.err
}

type batchCheck struct {
	next   func() (*batchRead, bool)
	stop   func()
	done   bool
	result authmodel.CheckResult
	err    error
}

// checkBatchTraversal changes only read scheduling. iter.Pull retains the
// ordinary evaluator's recursive stack between reads, including root-relative
// depth, cycle state, short circuits and step accounting. The iterators are
// advanced sequentially; they never concurrently access a reader/transaction.
// Every iterator is stopped on every exit, including cancellation and panic.
func (s *Service) checkBatchTraversal(ctx context.Context, source CheckReader, reqs []authmodel.CheckRequest) ([]authmodel.CheckResult, error) {
	if len(reqs) < 2 {
		return s.checkBatchSequential(ctx, source, reqs)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	memo := newMemoReader(source)
	checks := make([]batchCheck, len(reqs))
	defer func() {
		for i := range checks {
			if checks[i].stop != nil {
				checks[i].stop()
			}
		}
	}()
	for i, req := range reqs {
		check := &checks[i]
		check.next, check.stop = iter.Pull(func(yield func(*batchRead) bool) {
			reader := &memoReader{inner: yieldingReader{yield}, targets: memo.targets, direct: memo.direct}
			view := *s
			view.facts = yieldingFacts{Reader: s.facts, yield: yield}
			check.result, check.err = view.check(ctx, req, newBudget(s.limits, reader))
		})
	}

	end := len(checks)
	var firstErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var pending []*batchRead
		for i := 0; i < end; i++ {
			check := &checks[i]
			if check.done {
				continue
			}
			read, more := check.next()
			if more {
				pending = append(pending, read)
				continue
			}
			check.done = true
			if check.err != nil {
				// Finish only earlier requests, preserving the first failing
				// request's error in input order. This failed request and all
				// later ones perform no further reads.
				end, firstErr = i, check.err
				break
			}
		}
		if len(pending) == 0 {
			break
		}
		resolveBatchReads(ctx, source, s.facts, pending)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]authmodel.CheckResult, len(checks))
	for i := range checks {
		results[i] = checks[i].result
	}
	return results, nil
}

func resolveBatchReads(ctx context.Context, source CheckReader, facts tuples.Reader, pending []*batchRead) {
	exact := []*batchRead{}
	graph := []*batchRead{}
	for _, read := range pending {
		if read.tuple != nil {
			exact = append(exact, read)
		} else {
			graph = append(graph, read)
		}
	}
	if len(exact) > 0 {
		requested := make([]tuples.Tuple, len(exact))
		for i, read := range exact {
			requested[i] = *read.tuple
		}
		values, err := facts.ContainsMany(ctx, requested)
		if err == nil && len(values) != len(exact) {
			err = fmt.Errorf("invalid exact batch cardinality: %w", sdk.ErrUnavailable)
		}
		for i, read := range exact {
			read.err = err
			if err == nil {
				read.allowed = values[i]
			}
		}
	}
	pending = graph
	sets, ok := source.(RelationSetReader)
	if !ok {
		sets = &sequentialSets{CheckReader: source}
	}

	type groupKey struct {
		key    directKey // resourceID is empty: every other argument must agree
		direct bool
	}
	var groups [][]*batchRead
	indexes := make(map[groupKey]int)
	for _, read := range pending {
		key := groupKey{key: read.key, direct: read.direct}
		key.key.resourceID = ""
		index, ok := indexes[key]
		if !ok {
			index = len(groups)
			indexes[key] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], read)
	}
	// Groups retain first-request order, never map iteration order.
	for _, group := range groups {
		ids := make([]string, len(group))
		for i, read := range group {
			ids[i] = read.key.resourceID
		}
		ids = distinctSorted(ids)
		key := group[0].key
		err := ctx.Err()
		var targets map[string][]RelationTarget
		var matched []string
		if err == nil {
			if group[0].direct {
				matched, err = sets.FilterRelation(ctx, key.resourceType, ids, key.relation, key.subjectType, key.subjectID, key.maxExpansionStates)
			} else {
				targets, err = sets.RelationTargetsFor(ctx, key.resourceType, ids, key.relation)
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		allowed := make(map[string]bool, len(matched))
		for _, id := range matched {
			allowed[id] = true
		}
		for _, read := range group {
			read.err = err
			read.allowed = allowed[read.key.resourceID]
			read.targets = targets[read.key.resourceID]
		}
	}
}

type yieldingFacts struct {
	tuples.Reader
	yield func(*batchRead) bool
}

func (r yieldingFacts) Contains(ctx context.Context, t tuples.Tuple) (bool, error) {
	read := &batchRead{tuple: &t}
	if !r.yield(read) {
		return false, context.Canceled
	}
	return read.allowed, read.err
}

type sequentialSets struct{ CheckReader }

func (s *sequentialSets) FilterRelation(ctx context.Context, rt string, ids []string, rel, st, sid string, max int) ([]string, error) {
	values, err := s.CheckBatchDirect(ctx, rt, ids, rel, st, sid, max)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, id := range ids {
		if values[id] {
			out = append(out, id)
		}
	}
	return out, nil
}
func (s *sequentialSets) RelationTargetsFor(ctx context.Context, rt string, ids []string, rel string) (map[string][]RelationTarget, error) {
	out := map[string][]RelationTarget{}
	for _, id := range ids {
		values, err := s.GetRelationTargets(ctx, rt, id, rel)
		if err != nil {
			return nil, err
		}
		out[id] = values
	}
	return out, nil
}

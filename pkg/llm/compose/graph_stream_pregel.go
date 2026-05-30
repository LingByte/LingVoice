package compose

import (
	"context"
	"errors"
	"sync"

	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// streamPregelGraph runs Pregel BSP with token-level streaming at ChatModel supersteps.
func (r *CompiledGraph) streamPregelGraph(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil {
		return nil, errors.New("compose: nil compiled graph")
	}
	if len(r.chatModels) == 0 {
		return r.streamViaInvokePipe(ctx, messages, opts...)
	}
	outSR, outSW := schema.Pipe[*schema.Message](32)
	go func() {
		defer outSW.Close()
		st := &GraphState{
			Messages: append([]*schema.Message(nil), messages...),
			Vars:     map[string]any{},
		}
		if r.genLocalState != nil {
			ctx = initLocalState(ctx, r.genLocalState)
		}
		applyCompiledChannelSpecs(st, r.channelSpecs, r.pregelMerge)

		completed := map[string]bool{}
		scheduled := map[string]bool{}
		reachedEnd := false
		for _, e := range r.entries {
			scheduled[e] = true
		}

		for step := 0; step < r.maxSteps; step++ {
			if graphInterruptRequested(ctx) {
				outSW.Send(nil, errors.New("compose: pregel stream interrupted"))
				return
			}
			ready := r.pregelReady(completed, scheduled)
			if len(ready) == 0 {
				if reachedEnd {
					return
				}
				outSW.Send(nil, errPregelStalled(r.name, step))
				return
			}

			var syncNodes, modelNodes []string
			for _, n := range ready {
				if r.chatModels[n] != nil {
					modelNodes = append(modelNodes, n)
				} else {
					syncNodes = append(syncNodes, n)
				}
			}

			partials, err := r.bspSyncNodes(ctx, st, syncNodes)
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			for nodeName, partial := range partials {
				mergeNodeOutput(st, partial, nodeName)
				completed[nodeName] = true
				succs, err := r.successors(nodeName, ctx, st)
				if err != nil {
					outSW.Send(nil, err)
					return
				}
				postMailboxFromNode(st, nodeName, succs)
				for _, succ := range succs {
					if succ == END {
						reachedEnd = true
						continue
					}
					scheduled[succ] = true
				}
			}

			if len(modelNodes) == 0 {
				continue
			}

			streamPartials, err := r.bspStreamModelNodes(ctx, st, modelNodes, outSW, opts...)
			if err != nil {
				outSW.Send(nil, err)
				return
			}
			for nodeName, partial := range streamPartials {
				mergeNodeOutput(st, partial, nodeName)
				completed[nodeName] = true
				succs, err := r.successors(nodeName, ctx, st)
				if err != nil {
					outSW.Send(nil, err)
					return
				}
				postMailboxFromNode(st, nodeName, succs)
				for _, succ := range succs {
					if succ == END {
						reachedEnd = true
						continue
					}
					scheduled[succ] = true
				}
			}
		}
		outSW.Send(nil, errPregelMaxSteps(r.name, r.maxSteps))
	}()
	return outSR, nil
}

func (r *CompiledGraph) bspSyncNodes(ctx context.Context, st *GraphState, nodes []string) (map[string]*GraphState, error) {
	partials := make(map[string]*GraphState, len(nodes))
	if len(nodes) == 0 {
		return partials, nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	for _, nodeName := range nodes {
		fn, ok := r.nodes[nodeName]
		if !ok {
			return nil, errMissingNode(nodeName)
		}
		wg.Add(1)
		go func(name string, fn NodeFunc) {
			defer wg.Done()
			local := cloneGraphState(st)
			if err := applyMailboxToNode(local, name, r.preds[name]); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if err := fn(ctx, local); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			partials[name] = local
			mu.Unlock()
		}(nodeName, fn)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return partials, nil
}

func (r *CompiledGraph) bspStreamModelNodes(
	ctx context.Context,
	st *GraphState,
	nodes []string,
	outSW *schema.StreamWriter[*schema.Message],
	opts ...GraphInvokeOption,
) (map[string]*GraphState, error) {
	partials := make(map[string]*GraphState, len(nodes))
	var readers []*schema.StreamReader[*schema.Message]
	var waits []func() (*GraphState, error)
	var nodeNames []string

	for _, nodeName := range nodes {
		local := cloneGraphState(st)
		if err := applyMailboxToNode(local, nodeName, r.preds[nodeName]); err != nil {
			return nil, err
		}
		sr, wait := r.beginChatModelStream(ctx, nodeName, local, opts...)
		if sr == nil {
			if wait != nil {
				updated, err := wait()
				if err != nil {
					return nil, err
				}
				partials[nodeName] = updated
			}
			continue
		}
		readers = append(readers, sr)
		waits = append(waits, wait)
		nodeNames = append(nodeNames, nodeName)
	}

	if len(readers) > 0 {
		merged := MergeMessageStreams(readers...)
		defer merged.Close()
		for {
			chunk, err := merged.Recv()
			if err != nil {
				break
			}
			if !outSW.Send(chunk, nil) {
				break
			}
		}
	}

	for i, wait := range waits {
		if wait == nil {
			continue
		}
		updated, err := wait()
		if err != nil {
			return nil, err
		}
		partials[nodeNames[i]] = updated
	}
	return partials, nil
}

// HasPregelStreamCapability reports whether Pregel graphs can stream tokens in supersteps.
func (r *CompiledGraph) HasPregelStreamCapability() bool {
	if r == nil {
		return false
	}
	return (r.runMode == RunModePregel || len(r.fanOut) > 0) && len(r.chatModels) > 0
}

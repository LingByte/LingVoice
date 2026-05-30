package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

type streamNodeResult struct {
	local *GraphState
	err   error
}

// streamGraphWalk executes a DAG graph, streaming at the final ChatModel node on the path.
func (r *CompiledGraph) streamGraphWalk(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	if r == nil {
		return nil, errors.New("compose: nil compiled graph")
	}
	if r.react != nil {
		return r.Stream(ctx, messages, opts...)
	}
	if len(r.chatModels) == 0 {
		return r.streamViaInvokePipe(ctx, messages, opts...)
	}
	if r.runMode == RunModePregel || len(r.fanOut) > 0 {
		return r.streamPregelGraph(ctx, messages, opts...)
	}

	st := &GraphState{
		Messages: append([]*schema.Message(nil), messages...),
		Vars:     map[string]any{},
	}
	if r.genLocalState != nil {
		ctx = initLocalState(ctx, r.genLocalState)
	}
	applyCompiledChannelSpecs(st, nil, r.pregelMerge)

	current := r.entry
	var lastModelNode string
	for i := 0; i < r.maxSteps; i++ {
		if current == END {
			break
		}
		if _, ok := r.chatModels[current]; ok {
			lastModelNode = current
		}
		fn, ok := r.nodes[current]
		if !ok {
			return nil, fmt.Errorf("compose: graph missing node %q", current)
		}
		if _, isModel := r.chatModels[current]; !isModel {
			if err := fn(ctx, st); err != nil {
				return nil, err
			}
		}
		next, err := r.nextNode(current, ctx, st)
		if err != nil {
			return nil, err
		}
		current = next
	}
	if lastModelNode == "" {
		return r.streamViaInvokePipe(ctx, messages, opts...)
	}
	return r.streamChatModelNode(ctx, lastModelNode, st, opts...)
}

func (r *CompiledGraph) streamViaInvokePipe(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	msg, err := r.InvokeAsMessage(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return pipeSingleMessage(msg), nil
}

func (r *CompiledGraph) streamChatModelNode(ctx context.Context, nodeName string, st *GraphState, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], error) {
	sr, wait := r.beginChatModelStream(ctx, nodeName, st, opts...)
	if wait == nil {
		return sr, nil
	}
	go func() { _, _ = wait() }()
	return sr, nil
}

func (r *CompiledGraph) beginChatModelStream(ctx context.Context, nodeName string, st *GraphState, opts ...GraphInvokeOption) (*schema.StreamReader[*schema.Message], func() (*GraphState, error)) {
	model := r.chatModels[nodeName]
	if model == nil {
		return nil, nil
	}
	nodeOpts := r.chatModelOpts[nodeName]
	callOpts := mergeModelOpts(nodeOpts, chatModelOptsFromContext(ctx))
	icfg := applyInvokeOptions(opts...)
	for _, o := range icfg.chatModelOpts {
		callOpts = append(callOpts, o)
	}

	start := time.Now()
	sr, err := model.Stream(ctx, st.Messages, callOpts...)
	if err != nil {
		if errors.Is(err, llm.ErrNotImplemented) {
			out, genErr := model.Generate(ctx, st.Messages, callOpts...)
			recordGraphModelRun(st, model, st.Messages, out, start, genErr)
			if genErr != nil {
				return nil, func() (*GraphState, error) { return st, genErr }
			}
			st.LastOutput = out
			if out != nil {
				st.Messages = append(st.Messages, out)
			}
			return pipeSingleMessage(out), func() (*GraphState, error) { return st, nil }
		}
		recordGraphModelRun(st, model, st.Messages, nil, start, err)
		return nil, func() (*GraphState, error) { return st, err }
	}

	outSR, outSW := schema.Pipe[*schema.Message](32)
	done := make(chan streamNodeResult, 1)
	go func() {
		defer outSW.Close()
		defer sr.Close()
		var chunks []*schema.Message
		for {
			chunk, err := sr.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					outSW.Send(nil, err)
					done <- streamNodeResult{local: st, err: err}
					return
				}
				break
			}
			chunks = append(chunks, chunk)
			outSW.Send(chunk, nil)
		}
		full, concatErr := schema.ConcatMessages(chunks)
		recordGraphModelRun(st, model, st.Messages, full, start, concatErr)
		if concatErr != nil {
			done <- streamNodeResult{local: st, err: concatErr}
			return
		}
		st.LastOutput = full
		if full != nil {
			st.Messages = append(st.Messages, full)
		}
		done <- streamNodeResult{local: st, err: nil}
	}()
	wait := func() (*GraphState, error) {
		res := <-done
		return res.local, res.err
	}
	return outSR, wait
}

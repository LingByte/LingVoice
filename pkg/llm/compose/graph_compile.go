package compose

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/callback"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// START and END are graph sentinel nodes (Eino compose.START / END).
const (
	START = "start"
	END   = "end"
)

// GraphState is shared mutable state during graph execution.
type GraphState struct {
	Messages   []*schema.Message
	LastOutput *schema.Message
	Vars       map[string]any
}

// NodeFunc executes one graph node against state.
type NodeFunc func(ctx context.Context, st *GraphState) error

// BranchFunc selects the next node name from state.
type BranchFunc func(ctx context.Context, st *GraphState) (string, error)

// GraphRunMode controls compiled graph execution (Eino runTypePregel / runTypeDAG subset).
type GraphRunMode string

const (
	RunModeDAG    GraphRunMode = "DAG"
	RunModePregel GraphRunMode = "Pregel"
)

// Graph is a minimal cyclic graph builder (Eino compose.Graph subset).
type Graph struct {
	name          string
	nodes         map[string]NodeFunc
	edges         map[string]string
	fanOut        map[string][]string
	branches      map[string]BranchFunc
	branchOK      map[string]map[string]bool
	joinAny       map[string]bool
	nodeKinds     map[string]NodeKind
	maxSteps      int
	runMode       GraphRunMode
	genLocalState func(context.Context) any
	chatModels    map[string]llm.ChatModel
	chatModelOpts map[string][]llm.Option
}

// NewGraph creates an empty graph.
func NewGraph(name string) *Graph {
	return &Graph{
		name:     name,
		nodes:    make(map[string]NodeFunc),
		edges:    make(map[string]string),
		fanOut:   make(map[string][]string),
		branches: make(map[string]BranchFunc),
		branchOK: make(map[string]map[string]bool),
		joinAny:  make(map[string]bool),
		maxSteps: defaultMaxToolRounds * 2,
		runMode:  RunModeDAG,
	}
}

// WithRunMode sets graph execution mode (default DAG).
func (g *Graph) WithRunMode(mode GraphRunMode) *Graph {
	if g != nil && mode != "" {
		g.runMode = mode
	}
	return g
}

// MarkAnyPredecessorJoin marks fan-in nodes that fire when any predecessor completes.
func (g *Graph) MarkAnyPredecessorJoin(nodes ...string) *Graph {
	if g == nil {
		return g
	}
	if g.joinAny == nil {
		g.joinAny = map[string]bool{}
	}
	for _, n := range nodes {
		g.joinAny[n] = true
	}
	return g
}

// WithMaxSteps caps graph transitions (default 16).
func (g *Graph) WithMaxSteps(n int) *Graph {
	if g != nil && n > 0 {
		g.maxSteps = n
	}
	return g
}

// AddLambdaNode registers a node handler.
func (g *Graph) AddLambdaNode(name string, fn NodeFunc) error {
	if g == nil {
		return fmt.Errorf("compose: nil graph")
	}
	if name == "" || name == START || name == END {
		return fmt.Errorf("compose: invalid node name %q", name)
	}
	if fn == nil {
		return fmt.Errorf("compose: nil handler for node %q", name)
	}
	if _, dup := g.nodes[name]; dup {
		return fmt.Errorf("compose: duplicate node %q", name)
	}
	g.nodes[name] = fn
	if _, ok := g.nodeKinds[name]; !ok {
		g.markNodeKind(name, NodeKindLambda)
	}
	return nil
}

// AddEdge adds a fixed edge from → to (to may be END).
func (g *Graph) AddEdge(from, to string) error {
	if g == nil {
		return fmt.Errorf("compose: nil graph")
	}
	if from == END {
		return fmt.Errorf("compose: cannot add edge from END")
	}
	if to != END {
		if _, ok := g.nodes[to]; !ok && to != START {
			return fmt.Errorf("compose: unknown target node %q", to)
		}
	}
	if from != START {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("compose: unknown source node %q", from)
		}
	}
	if _, hasBranch := g.branches[from]; hasBranch {
		return fmt.Errorf("compose: node %q already has branch", from)
	}
	if len(g.fanOut[from]) > 0 {
		return fmt.Errorf("compose: node %q already has fan-out edges", from)
	}
	g.edges[from] = to
	return nil
}

// AddFanOutEdges registers multiple successors from one node (Eino parallel fan-out subset).
func (g *Graph) AddFanOutEdges(from string, to ...string) error {
	if g == nil {
		return fmt.Errorf("compose: nil graph")
	}
	if from == END {
		return fmt.Errorf("compose: cannot fan-out from END")
	}
	if from != START {
		if _, ok := g.nodes[from]; !ok {
			return fmt.Errorf("compose: unknown fan-out source %q", from)
		}
	}
	if _, hasEdge := g.edges[from]; hasEdge {
		return fmt.Errorf("compose: node %q already has single edge", from)
	}
	if _, hasBranch := g.branches[from]; hasBranch {
		return fmt.Errorf("compose: node %q already has branch", from)
	}
	if len(to) == 0 {
		return fmt.Errorf("compose: empty fan-out from %q", from)
	}
	for _, target := range to {
		if target != END {
			if _, ok := g.nodes[target]; !ok {
				return fmt.Errorf("compose: unknown fan-out target %q", target)
			}
		}
	}
	g.fanOut[from] = append([]string(nil), to...)
	if g.runMode == RunModeDAG {
		g.runMode = RunModePregel
	}
	return nil
}

// AddBranch adds conditional routing from a node.
func (g *Graph) AddBranch(from string, cond BranchFunc, allowed map[string]bool) error {
	if g == nil {
		return fmt.Errorf("compose: nil graph")
	}
	if from == START || from == END {
		return fmt.Errorf("compose: invalid branch source %q", from)
	}
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("compose: unknown branch source %q", from)
	}
	if cond == nil {
		return fmt.Errorf("compose: nil branch for %q", from)
	}
	if _, hasEdge := g.edges[from]; hasEdge {
		return fmt.Errorf("compose: node %q already has edge", from)
	}
	if len(g.fanOut[from]) > 0 {
		return fmt.Errorf("compose: node %q already has fan-out edges", from)
	}
	g.branches[from] = cond
	g.branchOK[from] = allowed
	return nil
}

// CompiledGraph is a runnable graph.
type CompiledGraph struct {
	name              string
	nodes             map[string]NodeFunc
	edges             map[string]string
	fanOut            map[string][]string
	branches          map[string]BranchFunc
	branchOK          map[string]map[string]bool
	joinAny           map[string]bool
	preds             map[string][]string
	entries           []string
	runMode           GraphRunMode
	entry             string
	maxSteps          int
	checkpointStore   CheckPointStore
	interruptBefore   map[string]bool
	interruptAfter    map[string]bool
	clearOnComplete   bool
	nodeKinds         map[string]NodeKind
	react             *reActRuntime
	genLocalState     func(context.Context) any
	pregelMerge       map[string]PregelChannelMerge
	channelSpecs      map[string]ChannelSpec
	chatModels        map[string]llm.ChatModel
	chatModelOpts     map[string][]llm.Option
	transformMode     TransformMode
}

// HasReActRuntime reports whether native ReAct streaming is available.
func (r *CompiledGraph) HasReActRuntime() bool {
	return r != nil && r.react != nil
}

// Compile validates and returns a runnable graph.
func (g *Graph) Compile(opts ...GraphCompileOption) (*CompiledGraph, error) {
	if g == nil {
		return nil, fmt.Errorf("compose: nil graph")
	}
	var entries []string
	if tos, ok := g.fanOut[START]; ok && len(tos) > 0 {
		entries = append([]string(nil), tos...)
	} else if entry, ok := g.edges[START]; ok {
		if entry == END {
			return nil, fmt.Errorf("compose: graph %q START cannot point to END", g.name)
		}
		entries = []string{entry}
	} else {
		return nil, fmt.Errorf("compose: graph %q missing edge from START", g.name)
	}
	var cfg graphCompileConfig
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	applyNodeTriggerModes(g, &cfg)
	runMode := g.runMode
	if cfg.runMode != "" {
		runMode = cfg.runMode
	}
	if len(g.fanOut) > 0 && runMode == RunModeDAG {
		runMode = RunModePregel
	}
	preds := computePredecessors(g)
	cg := &CompiledGraph{
		name:            g.name,
		nodes:           g.nodes,
		edges:           g.edges,
		fanOut:          g.fanOut,
		branches:        g.branches,
		branchOK:        g.branchOK,
		joinAny:         g.joinAny,
		preds:           preds,
		entries:         entries,
		runMode:         runMode,
		entry:           entries[0],
		maxSteps:        g.maxSteps,
		checkpointStore: cfg.checkpointStore,
		interruptBefore: cfg.interruptBefore,
		interruptAfter:  cfg.interruptAfter,
		clearOnComplete: cfg.clearOnComplete,
		nodeKinds:       cloneNodeKinds(g.nodeKinds),
		genLocalState:   g.genLocalState,
		pregelMerge:     clonePregelMerge(cfg.pregelMerge),
		channelSpecs:    cloneChannelSpecs(cfg.channelSpecs),
		chatModels:      cloneChatModels(g.chatModels),
		chatModelOpts:   cloneChatModelOpts(g.chatModelOpts),
		transformMode:   cfg.transformMode,
	}
	if cfg.compileCallback != nil {
		if err := cfg.compileCallback(context.Background(), cg.Describe()); err != nil {
			return nil, fmt.Errorf("compose: compile callback: %w", err)
		}
	}
	return cg, nil
}

// Invoke runs the graph until END, interrupt, or max steps.
func (r *CompiledGraph) Invoke(ctx context.Context, messages []*schema.Message, opts ...GraphInvokeOption) (*GraphState, []GraphStep, error) {
	if r == nil {
		return nil, nil, fmt.Errorf("compose: nil compiled graph")
	}
	icfg := applyInvokeOptions(opts...)
	if len(icfg.resumeData) > 0 {
		ctx = BatchResumeWithData(ctx, icfg.resumeData)
	}
	if len(icfg.chatModelOpts) > 0 {
		ctx = withGraphChatModelOpts(ctx, icfg.chatModelOpts)
	}

	ctx = callback.InitRun(ctx, &callback.RunInfo{Name: r.name, Component: callback.ComponentGraph})
	if r.genLocalState != nil {
		ctx = initLocalState(ctx, r.genLocalState)
	}

	st := &GraphState{
		Messages: append([]*schema.Message(nil), messages...),
		Vars:     map[string]any{},
	}
	var trace []GraphStep
	current := r.entry
	var resumeCtx *InterruptCtx
	skipInterruptBefore := ""
	skipInterruptAfter := map[string]bool{}
	var pregelStart *PregelCheckpoint

	if icfg.checkpointID != "" {
		cp, ok, err := r.loadCheckpoint(ctx, icfg.checkpointID)
		if err != nil {
			return st, trace, err
		}
		if ok && cp != nil {
			if cp.NextNode == END && cp.Interrupt == nil && len(icfg.resumeData) == 0 {
				cp = nil
			}
		}
		if ok && cp != nil {
			if cp.State != nil {
				st = cp.State
			}
			if cp.Pregel != nil {
				pregelStart = cp.Pregel
			}
			if cp.NextNode != "" && cp.NextNode != pregelCheckpointMarker {
				current = cp.NextNode
			}
			if cp.Interrupt != nil {
				if len(cp.Interrupt.InterruptContexts) > 0 {
					resumeCtx = cp.Interrupt.InterruptContexts[0]
					ctx = withInterruptResume(ctx, resumeCtx)
				}
				for _, n := range cp.Interrupt.AfterNodes {
					skipInterruptAfter[n] = true
				}
				if cp.Pregel != nil && len(cp.Interrupt.BeforeNodes) > 0 {
					skipInterruptBefore = cp.Interrupt.BeforeNodes[0]
				} else {
					for _, n := range cp.Interrupt.BeforeNodes {
						if n == current {
							skipInterruptBefore = n
							break
						}
					}
				}
			}
		}
	}

	if icfg.stateModifier != nil {
		if err := icfg.stateModifier(ctx, st); err != nil {
			return st, trace, err
		}
	}
	applyCompiledChannelSpecs(st, r.channelSpecs, r.pregelMerge)

	if r.runMode == RunModePregel || len(r.fanOut) > 0 {
		return r.invokePregelViaGraphRun(ctx, st, trace, icfg, pregelStart, skipInterruptBefore)
	}

	for i := 0; i < r.maxSteps; i++ {
		if graphInterruptRequested(ctx) {
			info := &InterruptInfo{
				State:       cloneGraphState(st),
				RerunNodes:  []string{current},
				InterruptContexts: []*InterruptCtx{{
					ID:   newInterruptID(),
					Node: current,
					Info: map[string]string{"reason": "external_interrupt"},
				}},
			}
			_ = r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
				NextNode: current, State: cloneGraphState(st), Interrupt: info,
			})
			return st, trace, wrapInterruptInfo(info)
		}
		if current == END {
			if icfg.checkpointID != "" {
				if icfg.clearCheckpoint || r.clearOnComplete {
					r.deleteCheckpoint(ctx, icfg.checkpointID)
				} else {
					_ = r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
						NextNode: END,
						State:    cloneGraphState(st),
					})
				}
			}
			return st, trace, nil
		}
		fn, ok := r.nodes[current]
		if !ok {
			return st, trace, fmt.Errorf("compose: graph missing node %q", current)
		}

		if r.interruptBefore != nil && r.interruptBefore[current] && current != skipInterruptBefore {
			info := &InterruptInfo{
				State:       cloneGraphState(st),
				BeforeNodes: []string{current},
				InterruptContexts: []*InterruptCtx{{
					ID:   newInterruptID(),
					Node: current,
					Info: map[string]string{"phase": "before", "node": current},
				}},
			}
			if err := r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
				NextNode:  current,
				State:     cloneGraphState(st),
				Interrupt: info,
			}); err != nil {
				return st, trace, err
			}
			return st, trace, wrapInterruptInfo(info)
		}

		start := time.Now()
		nodeCtx := ctx
		if resumeCtx != nil && resumeCtx.Node == current {
			nodeCtx = withInterruptResume(ctx, resumeCtx)
		}
		if err := fn(nodeCtx, st); err != nil {
			var ie *InterruptError
			if errors.As(err, &ie) {
				if ie.ID == "" {
					ie.ID = newInterruptID()
				}
				if ie.Node == "" {
					ie.Node = current
				}
				info := &InterruptInfo{
					State:       cloneGraphState(st),
					InterruptContexts: []*InterruptCtx{{
						ID:    ie.ID,
						Node:  current,
						Info:  ie.Info,
						State: ie.State,
					}},
				}
				if err := r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
					NextNode:  current,
					State:     cloneGraphState(st),
					Interrupt: info,
				}); err != nil {
					return st, trace, err
				}
				trace = append(trace, GraphStep{Node: current, StartedAt: start, Duration: time.Since(start).String()})
				return st, trace, wrapInterruptInfo(info)
			}
			return st, trace, fmt.Errorf("compose: node %q: %w", current, err)
		}
		resumeCtx = nil
		if skipInterruptBefore == current {
			skipInterruptBefore = ""
		}
		delete(skipInterruptAfter, current)
		trace = append(trace, GraphStep{Node: current, StartedAt: start, Duration: time.Since(start).String()})

		next, err := r.nextNode(current, ctx, st)
		if err != nil {
			return st, trace, err
		}

		if r.interruptAfter != nil && r.interruptAfter[current] && !skipInterruptAfter[current] {
			info := &InterruptInfo{
				State:      cloneGraphState(st),
				AfterNodes: []string{current},
				InterruptContexts: []*InterruptCtx{{
					ID:   newInterruptID(),
					Node: current,
					Info: map[string]string{"phase": "after", "node": current, "next": next},
				}},
			}
			if err := r.saveCheckpoint(ctx, icfg.checkpointID, &Checkpoint{
				NextNode:  next,
				State:     cloneGraphState(st),
				Interrupt: info,
			}); err != nil {
				return st, trace, err
			}
			return st, trace, wrapInterruptInfo(info)
		}

		current = next
	}
	return st, trace, fmt.Errorf("compose: graph %q exceeded max steps (%d)", r.name, r.maxSteps)
}

func (r *CompiledGraph) nextNode(from string, ctx context.Context, st *GraphState) (string, error) {
	if cond, ok := r.branches[from]; ok {
		to, err := cond(ctx, st)
		if err != nil {
			return "", err
		}
		if to == END {
			return END, nil
		}
		allowed := r.branchOK[from]
		if allowed != nil && !allowed[to] {
			return "", fmt.Errorf("compose: branch from %q to disallowed node %q", from, to)
		}
		return to, nil
	}
	to, ok := r.edges[from]
	if !ok {
		return "", fmt.Errorf("compose: node %q has no edge or branch", from)
	}
	return to, nil
}

func clonePregelMerge(in map[string]PregelChannelMerge) map[string]PregelChannelMerge {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]PregelChannelMerge, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneChatModels(in map[string]llm.ChatModel) map[string]llm.ChatModel {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]llm.ChatModel, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneChatModelOpts(in map[string][]llm.Option) map[string][]llm.Option {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]llm.Option, len(in))
	for k, v := range in {
		out[k] = append([]llm.Option(nil), v...)
	}
	return out
}

func (r *CompiledGraph) deleteCheckpoint(ctx context.Context, id string) {
	if r == nil || id == "" {
		return
	}
	if d, ok := r.checkpointStore.(CheckPointDeleter); ok {
		_ = d.Delete(ctx, id)
	}
}

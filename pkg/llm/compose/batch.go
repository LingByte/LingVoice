package compose

import (
	"context"
	"fmt"
	"sync"
)

// Batch runs multiple sub-processes with composite interrupt support (Eino batch pattern).
type Batch struct {
	Name  string
	Items []BatchItem
}

// BatchItem is one batch entry with its own address segment.
type BatchItem struct {
	ID  string
	Run func(ctx context.Context) error
}

// Run executes all items, collecting composite interrupts.
func (b *Batch) Run(ctx context.Context) error {
	if b == nil || len(b.Items) == 0 {
		return nil
	}
	errs := make([]error, len(b.Items))
	var wg sync.WaitGroup
	for i, item := range b.Items {
		if item.Run == nil {
			continue
		}
		wg.Add(1)
		go func(i int, item BatchItem) {
			defer wg.Done()
			seg := item.ID
			if seg == "" {
				seg = fmtIndex(i)
			}
			itemCtx := AppendAddressSegment(ctx, seg)
			errs[i] = item.Run(itemCtx)
		}(i, item)
	}
	wg.Wait()
	return CollectInterrupts(errs...)
}

func fmtIndex(i int) string {
	return fmt.Sprintf("item-%d", i)
}

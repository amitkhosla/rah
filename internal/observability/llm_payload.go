package observability

// LLMCallEntry holds the complete data for one LLM call captured during request execution.
// The raw bytes (ReqBytes, ResBytes) are retained on the context until copy-out
// (see payloadSlabRing in payload_slab.go); metadata fields mirror LLMCallRow.
type LLMCallEntry struct {
	PC           int16
	Seq          uint8
	ModelName    string // points into compiled string, no alloc
	Status       uint16
	InputTokens  uint32
	OutputTokens uint32
	CostMicro    uint32
	DurationNs   int64
	ReqBytes     []byte // raw request JSON (already marshaled upstream)
	ResBytes     []byte // raw response bytes (from io.ReadAll or streaming tee)
}

// LLMCallBlock is one node in a linked chain of LLM call entries.
// Blocks of 16 entries handle agentic loops with 50+ calls without pre-allocation.
type LLMCallBlock struct {
	Entries [16]LLMCallEntry
	Count   uint8
	Next    *LLMCallBlock
}

// AppendLLMCall adds an entry to the linked block chain rooted at *head.
// Allocates a new block when the current tail block is full.
func AppendLLMCall(head **LLMCallBlock, entry LLMCallEntry) {
	if *head == nil {
		*head = &LLMCallBlock{}
	}
	b := *head
	for b.Next != nil {
		b = b.Next
	}
	if int(b.Count) < len(b.Entries) {
		b.Entries[b.Count] = entry
		b.Count++
		return
	}
	nb := &LLMCallBlock{}
	nb.Entries[0] = entry
	nb.Count = 1
	b.Next = nb
}

// LLMCallRows extracts the metadata-only []LLMCallRow slice from the linked chain,
// for embedding in a TraceRecord. Called at request end, before ctx.Reset().
func LLMCallRows(head *LLMCallBlock) []LLMCallRow {
	if head == nil {
		return nil
	}
	var count int
	for b := head; b != nil; b = b.Next {
		count += int(b.Count)
	}
	rows := make([]LLMCallRow, 0, count)
	for b := head; b != nil; b = b.Next {
		for i := range b.Count {
			e := &b.Entries[i]
			rows = append(rows, LLMCallRow{
				PC:           e.PC,
				Seq:          e.Seq,
				ModelName:    e.ModelName,
				Status:       e.Status,
				InputTokens:  e.InputTokens,
				OutputTokens: e.OutputTokens,
				CostMicro:    e.CostMicro,
				DurationNs:   e.DurationNs,
			})
		}
	}
	return rows
}

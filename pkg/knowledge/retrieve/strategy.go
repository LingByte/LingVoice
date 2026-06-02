package retrieve

// Strategy selects how indexed knowledge is queried.
type Strategy string

const (
	// StrategyVector uses dense vector similarity (Qdrant/Milvus).
	StrategyVector Strategy = "vector"
	// StrategyKeyword uses full-text search (Bleve via pkg/search).
	StrategyKeyword Strategy = "keyword"
	// StrategyHybrid merges vector and keyword scores.
	StrategyHybrid Strategy = "hybrid"
)

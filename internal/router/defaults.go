package router

import "time"

// 路由引擎内部融合、软降权熔断、独立否定法定数与上下文切片调优阈值。
const (
	defaultFusionFetchFloor        = 10
	defaultFusionMinGeneralGroups  = 2
	defaultFusionStragglerGrace    = 3 * time.Second
	defaultCircuitFailureThreshold = 3
	defaultCircuitCooldown         = 45 * time.Second
	defaultAccountFailureCooldown  = 5 * time.Minute
	fetchNotFoundQuorum            = 2
	searchEmptyQuorum              = 2
	maxOutlineSections             = 24
	maxMatchSnippets               = 8
	defaultViewMissPreviewChars    = 2000
	maxWholeFenceMatchRunes        = 1500
	codeMatchContextLines          = 12
	maxCodeMatchWindows            = 5
	compactBudgetSnippetRunes      = 100
	rrfScoreEpsilon                = 1e-9
)

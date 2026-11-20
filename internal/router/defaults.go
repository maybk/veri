package router

import "time"

// 路由引擎内部融合、软降权熔断、独立否定法定数与上下文切片调优阈值。
const (
	defaultFusionFetchFloor        = 10
	defaultFusionStragglerGrace    = 3 * time.Second
	defaultCircuitFailureThreshold = 3
	defaultCircuitCooldown         = 45 * time.Second
	fetchNotFoundQuorum            = 2
	searchEmptyQuorum              = 2
	maxOutlineSections             = 24
	maxMatchSnippets               = 8
	rrfScoreEpsilon                = 1e-9
)

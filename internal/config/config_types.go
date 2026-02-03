package config

type ResourceLimit struct {
	MaxHeaderSize  int   // e.g., 16KB
	MaxHeaderCount int   // e.g., 50
	MaxBodySize    int64 // e.g., 1MB (SME) vs 1GB (Enterprise)
}

type GlobalLayout struct {
	MaxBytes      int
	MaxInts       int
	MaxBools      int
	DefaultLimits ResourceLimit
}

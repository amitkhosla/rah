package control

import (
	"rah/internal/cache"
	"rah/internal/rctx"
)

func CacheLookup(ctx *rctx.Context, slabMgr *cache.CacheManager) bool {
	// ctx.Path is []byte. No conversion needed here!
	data, found := slabMgr.Get(ctx.TenantID, ctx.Path)
	if !found {
		return false
	}

	ctx.ResponseBuffer = data
	ctx.IsBuffered = true
	return true
}

func CacheStore(ctx *rctx.Context, slabMgr *cache.CacheManager, ttl uint32) {
	if !ctx.IsBuffered || len(ctx.ResponseBuffer) == 0 {
		return
	}

	// Direct pass-through of []byte
	_ = slabMgr.Put(ctx.TenantID, ctx.Path, ctx.ResponseBuffer, ttl)
}

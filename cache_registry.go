package mongoplus

import (
	"runtime"
	"sync"
	"weak"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// 只共享失效通知，不共享查询结果；不同 Database 选项和解码配置保持独立。
// 弱引用与 cleanup 避免全局注册表长期持有 Client/Database。
type cacheIdentity struct {
	client weak.Pointer[mongo.Client]
	name   string
}

type cachePeers struct {
	members  map[weak.Pointer[Database]]struct{}
	disabled bool // 外部 Session 的提交时刻不可知，后注册的包装器也必须绕过缓存。
}

var cacheRegistry = struct {
	sync.Mutex
	groups map[cacheIdentity]*cachePeers
}{groups: make(map[cacheIdentity]*cachePeers)}

type cacheRegistration struct {
	identity cacheIdentity
	member   weak.Pointer[Database]
}

func (d *Database) cacheIdentity() cacheIdentity {
	return cacheIdentity{client: weak.Make(d.native.Client()), name: d.native.Name()}
}

func registerCache(d *Database) {
	if d.native == nil {
		return
	}
	registration := cacheRegistration{identity: d.cacheIdentity(), member: weak.Make(d)}
	cacheRegistry.Lock()
	group := cacheRegistry.groups[registration.identity]
	if group == nil {
		group = &cachePeers{members: make(map[weak.Pointer[Database]]struct{})}
		cacheRegistry.groups[registration.identity] = group
	}
	group.members[registration.member] = struct{}{}
	d.cache.disabled = group.disabled
	cacheRegistry.Unlock()
	runtime.AddCleanup(d, unregisterCache, registration)
}

func unregisterCache(registration cacheRegistration) {
	cacheRegistry.Lock()
	defer cacheRegistry.Unlock()
	if group := cacheRegistry.groups[registration.identity]; group != nil {
		delete(group.members, registration.member)
		if len(group.members) == 0 {
			delete(cacheRegistry.groups, registration.identity)
		}
	}
}

// invalidatePeers 在操作返回前通知同 Client 同库的所有存活包装器。
func (d *Database) invalidatePeers(collection string, disable bool) {
	peers := []*Database{d}
	if d.native != nil {
		cacheRegistry.Lock()
		if group := cacheRegistry.groups[d.cacheIdentity()]; group != nil {
			group.disabled = group.disabled || disable
			for member := range group.members {
				if peer := member.Value(); peer != nil && peer != d {
					peers = append(peers, peer)
				}
			}
		}
		cacheRegistry.Unlock()
	}
	for _, peer := range peers {
		c := &peer.cache
		c.mu.Lock()
		if c.epochs == nil {
			c.epochs = make(map[string]uint64)
		}
		c.epochs[collection]++
		if disable {
			c.disabled = true
			c.entries = nil
			c.bytes = 0
		} else {
			for key, entry := range c.entries {
				if entry.scope == collection {
					c.remove(key)
				}
			}
		}
		c.mu.Unlock()
	}
	deleteSharedCache(peers, collection)
	runtime.KeepAlive(d)
}

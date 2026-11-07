package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/adapter/inbound"
	"github.com/metacubex/mihomo/component/profile/cachefile"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/updater"
	"github.com/metacubex/mihomo/config"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/constant/features"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/listener"
	"github.com/metacubex/mihomo/tunnel"
	"github.com/metacubex/mihomo/tunnel/statistic"
)

var (
	isInit     atomic.Bool
	isRunning  atomic.Bool
	sdkVersion atomic.Int32
)

var appCachePath string

func handleInit(params *InitParams) error {
	configMu.Lock()
	defer configMu.Unlock()
	if params.CachePath != "" {
		if !filepath.IsAbs(params.CachePath) {
			return errors.New("cache path must be absolute")
		}
		if err := os.MkdirAll(filepath.Dir(params.CachePath), 0700); err != nil {
			return err
		}
		if (isInit.Load() && constant.Path.Cache() != params.CachePath) || (appCachePath != "" && appCachePath != params.CachePath) {
			return errors.New("cache path cannot change while the core is running")
		}
		if appCachePath == "" {
			constant.SetCache(params.CachePath)
			cache := cachefile.Cache()
			if cache.DB == nil || cache.DB.Path() != params.CachePath {
				return errors.New("cache database did not open at the requested path")
			}
			appCachePath = params.CachePath
		}
	}
	sdkVersion.Store(int32(params.Version))
	constant.SetHomeDir(params.HomeDir)
	initOwnership(params.HomeDir)
	clearSpilledResponses()
	isInit.Store(true)
	return nil
}

func handleInitialized() bool {
	return isInit.Load()
}

func setRunning(running bool) {
	isRunning.Store(running)
}

func handleStartListener() {
	configMu.Lock()
	defer configMu.Unlock()
	setRunning(true)
	updateListeners(currentConfig)
	resolver.ResetConnection()
	// A probe already out left before the TUN routes and over the DNS
	// connections just dropped, so its answer must not stand for this route.
	bumpRouteEpoch()
}

func handleStopListener() {
	configMu.Lock()
	defer configMu.Unlock()
	setRunning(false)
	listener.StopListener()
	resolver.ResetConnection()
}

func updateListeners(cfg *config.Config) {
	if cfg == nil || !isRunning.Load() {
		return
	}
	general := cfg.General
	listener.PatchInboundListeners(cfg.Listeners, tunnel.Tunnel, true)

	listener.SetAllowLan(general.AllowLan)
	inbound.SetSkipAuthPrefixes(general.SkipAuthPrefixes)
	inbound.SetAllowedIPs(general.LanAllowedIPs)
	inbound.SetDisAllowedIPs(general.LanDisAllowedIPs)

	listener.SetBindAddress(general.BindAddress)
	listener.ReCreateHTTP(general.Port, tunnel.Tunnel)
	listener.ReCreateSocks(general.SocksPort, tunnel.Tunnel)
	listener.ReCreateRedir(general.RedirPort, tunnel.Tunnel)
	listener.ReCreateTProxy(general.TProxyPort, tunnel.Tunnel)
	listener.ReCreateMixed(general.MixedPort, tunnel.Tunnel)
	listener.ReCreateShadowSocks(general.ShadowSocksConfig, tunnel.Tunnel)
	listener.ReCreateVmess(general.VmessConfig, tunnel.Tunnel)
	listener.ReCreateTuic(general.TuicServer, tunnel.Tunnel)
	if !features.Android {
		listener.ReCreateTun(general.Tun, tunnel.Tunnel)
	}
}

func handleShutdown() {
	handleStopLog()
	stopRouteWatch()

	configMu.Lock()
	setRunning(false)
	listener.StopListener()
	updater.StopGeoUpdater()
	executor.Shutdown()
	currentConfig = nil
	appliedDelayDigest = ""
	preparedDelayDigest = ""
	isInit.Store(false)
	configMu.Unlock()

	handleForceGC()
}

func handleForceGC() {
	tunnel.InvalidateAllProxies()
	debug.FreeOSMemory()
}

// mihomo loads providers without waiting for them, so its own GC misses their decode buffers.
var (
	loadReleaseDelay  = 2 * time.Second
	releaseLoadMemory = debug.FreeOSMemory

	loadReleaseMu    sync.Mutex
	loadReleaseTimer *time.Timer
)

func scheduleLoadMemoryRelease() {
	loadReleaseMu.Lock()
	defer loadReleaseMu.Unlock()
	if loadReleaseTimer == nil {
		loadReleaseTimer = time.AfterFunc(loadReleaseDelay, func() { releaseLoadMemory() })
		return
	}
	loadReleaseTimer.Reset(loadReleaseDelay)
}

// HeapIdle still counts spans the runtime has already handed back to the OS,
// so the retained-but-unused figure subtracts HeapReleased.
func handleGetMemoryStats() MemoryStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return MemoryStats{
		Rss:          statistic.DefaultManager.Memory(),
		HeapInuse:    stats.HeapInuse,
		HeapIdle:     stats.HeapIdle - stats.HeapReleased,
		StackInuse:   stats.StackInuse,
		RuntimeOther: stats.MSpanInuse + stats.MCacheInuse + stats.BuckHashSys + stats.GCSys + stats.OtherSys,
	}
}

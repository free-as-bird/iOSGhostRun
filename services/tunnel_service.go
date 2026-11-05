package services

import (
	"context"
	"fmt"
	"iOSGhostRun/services/wintun"
	"sync"
	"time"

	"github.com/Masterminds/semver"
	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/tunnel"
)

var globalTunnelManager *tunnel.TunnelManager
var globalTunnelCancel context.CancelFunc
var globalTunnelDone <-chan struct{}
var tunnelStateMu sync.Mutex
var tunnelStartMu sync.Mutex
var globalTunnelUserspace bool
var tunnelElevationRestart func()
var tunnelElevationConfirm func() bool

// SetTunnelElevationConfirmation 注册启动管理员权限请求前的确认回调。
func SetTunnelElevationConfirmation(confirm func() bool) {
	tunnelStateMu.Lock()
	defer tunnelStateMu.Unlock()
	tunnelElevationConfirm = confirm
}

// SetTunnelElevationRestart 注册完成提权后退出旧进程的回调。
func SetTunnelElevationRestart(restart func()) {
	tunnelStateMu.Lock()
	defer tunnelStateMu.Unlock()
	tunnelElevationRestart = restart
}

// startTunnel 启动隧道管理器
func startTunnel(userspace bool) (<-chan struct{}, error) {
	pairRecordPath := ResolveAppDir("pairrecords")

	pm, err := tunnel.NewPairRecordManager(pairRecordPath)
	if err != nil {
		return nil, err
	}

	tm := tunnel.NewTunnelManager(pm, userspace)

	tunnelCtx, cancelTunnel := context.WithCancel(context.Background())

	tunnelStateMu.Lock()
	if globalTunnelManager != nil {
		tunnelStateMu.Unlock()
		cancelTunnel()
		tm.Close()
		return nil, nil
	}
	globalTunnelManager = tm
	globalTunnelUserspace = userspace
	globalTunnelCancel = cancelTunnel
	tunnelDone := make(chan struct{})
	globalTunnelDone = tunnelDone
	tunnelStateMu.Unlock()

	var updater sync.WaitGroup
	updater.Add(1)
	go func() {
		defer updater.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if err := tm.UpdateTunnels(tunnelCtx); err != nil && tunnelCtx.Err() == nil {
				Log.Debug("TunnelService", "更新隧道失败: "+err.Error())
			}
			select {
			case <-tunnelCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// 后台等待停止信号并清理
	go func() {
		<-tunnelCtx.Done()

		updater.Wait()
		tm.Close()

		tunnelStateMu.Lock()
		globalTunnelManager = nil
		globalTunnelCancel = nil
		globalTunnelDone = nil
		close(tunnelDone)
		tunnelStateMu.Unlock()
	}()

	return tunnelDone, nil
}

// StopTunnel 停止 tunnel
func StopTunnel() error {
	tunnelStateMu.Lock()
	cancel := globalTunnelCancel
	done := globalTunnelDone
	tunnelStateMu.Unlock()

	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
	return nil
}

// 当前 go-ios 的 USB 用户态隧道只支持高于 17.4.0 的版本。
func useUserspaceTunnel(version *semver.Version) bool {
	return version.GreaterThan(semver.MustParse("17.4.0"))
}

func ensureTunnelReady(udid string, version *semver.Version) error {
	tunnelStartMu.Lock()
	defer tunnelStartMu.Unlock()

	userspace := useUserspaceTunnel(version)
	if !userspace {
		if err := tunnel.CheckPermissions(); err != nil {
			tunnelStateMu.Lock()
			restart := tunnelElevationRestart
			confirm := tunnelElevationConfirm
			tunnelStateMu.Unlock()
			if restart == nil {
				return fmt.Errorf("系统隧道权限不足，提权重启尚未初始化: %w", err)
			}
			requested, elevationErr := wintun.RequestElevation(confirm)
			if elevationErr != nil {
				return elevationErr
			}
			if requested {
				go restart()
				return fmt.Errorf("正在以管理员身份重启，请重新选择设备")
			}
			return fmt.Errorf("iOS %s 需要使用系统隧道，请以管理员或 root 权限运行，或在 Linux 授予 CAP_NET_ADMIN: %w", version, err)
		}
		client := newDownloadHTTPClient(2 * time.Minute)
		defer client.CloseIdleConnections()
		if err := wintun.Prepare(ResolveAppDir("wintun"), client, func(message string) {
			Log.Info("TunnelService", message)
		}); err != nil {
			return err
		}
	}
	tunnelStateMu.Lock()
	needStart := globalTunnelManager == nil
	modeChanged := !needStart && globalTunnelUserspace != userspace
	tunnelStateMu.Unlock()
	if modeChanged {
		if err := StopTunnel(); err != nil {
			return fmt.Errorf("切换隧道模式失败: %w", err)
		}
		needStart = true
	}
	if needStart {
		if userspace {
			Log.Info("TunnelService", "启用用户态隧道")
		} else {
			Log.Info("TunnelService", "启用系统隧道")
		}
		if _, err := startTunnel(userspace); err != nil {
			return fmt.Errorf("启动隧道服务失败: %w", err)
		}
	}

	if err := waitTunnelDeviceReady(udid, 15*time.Second); err != nil {
		return fmt.Errorf("等待隧道就绪失败: %w", err)
	}
	return nil
}

func waitTunnelDeviceReady(udid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if _, err := getTunnelDevice(udid); err == nil {
			return nil
		} else {
			lastErr = err
		}

		time.Sleep(300 * time.Millisecond)
	}

	if lastErr != nil {
		return fmt.Errorf("设备隧道未就绪: %w", lastErr)
	}

	return fmt.Errorf("设备隧道未就绪")
}

// getTunnelDevice 获取隧道设备及其 RSD 服务信息
func getTunnelDevice(udid string) (*ios.DeviceEntry, error) {
	tunnelStateMu.Lock()
	manager := globalTunnelManager
	tunnelStateMu.Unlock()
	if manager == nil {
		return nil, fmt.Errorf("隧道服务尚未启动")
	}
	tunnelInfo, err := manager.FindTunnel(udid)
	if err != nil {
		return nil, fmt.Errorf("获取隧道信息失败: %w", err)
	}
	if tunnelInfo.Udid == "" {
		return nil, fmt.Errorf("设备隧道尚未就绪")
	}

	// 先获取基础设备信息
	baseDevice, err := ios.GetDevice(udid)
	if err != nil {
		return nil, fmt.Errorf("获取设备信息失败: %w", err)
	}

	// 用户态隧道需要通过本机转发连接 RSD 和设备服务。
	baseDevice.UserspaceTUN = tunnelInfo.UserspaceTUN
	baseDevice.UserspaceTUNPort = tunnelInfo.UserspaceTUNPort
	baseDevice.UserspaceTUNHost = "localhost"

	// 连接到 RSD 服务获取服务端口映射
	rsdService, err := ios.NewWithAddrPortDevice(tunnelInfo.Address, tunnelInfo.RsdPort, baseDevice)
	if err != nil {
		return nil, fmt.Errorf("连接 RSD 服务失败: %w", err)
	}
	defer rsdService.Close()

	// 执行握手获取 RsdPortProvider
	rsdProvider, err := rsdService.Handshake()
	if err != nil {
		return nil, fmt.Errorf("RSD 握手失败: %w", err)
	}

	// 复用已获取的设备，避免再次枚举 USB 设备。
	baseDevice.Address = tunnelInfo.Address
	baseDevice.Rsd = rsdProvider
	return &baseDevice, nil
}

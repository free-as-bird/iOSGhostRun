package services

import (
	"fmt"
	"strings"
	"sync"

	"github.com/danielpaulus/go-ios/ios"
)

// DevicesService 设备管理服务
type DevicesService struct {
	mu             sync.RWMutex
	selectedUDID   string
	runningService *RunningService
}

func NewDevicesService(runningService *RunningService) *DevicesService {
	return &DevicesService{runningService: runningService}
}

// ListDevices 获取可连接设备列表
func (d *DevicesService) ListDevices() ([]DeviceInfo, error) {
	Log.Info("DevicesService", "列出已连接的设备...")
	list, err := ios.ListDevices()
	if err != nil {
		Log.Error("DevicesService", "列出设备失败: "+err.Error())
		return nil, err
	}

	devices := make([]DeviceInfo, 0)
	var deviceHint string
	var lastDeviceErr error
	for _, entry := range list.DeviceList {
		// 仅接受 USB 连接的设备，过滤掉 Network 或其他连接类型
		connectionType := entry.ConnectionTypeLabel()
		if strings.ToLower(connectionType) != "usb" {
			Log.Debug("DevicesService", fmt.Sprintf("跳过非 USB 连接设备: %s (连接类型: %s)", entry.Properties.SerialNumber, connectionType))
			continue
		}

		udid := entry.Properties.SerialNumber
		info, err := getDeviceInfo(entry)
		if err != nil {
			lastDeviceErr = err
			Log.Debug("DevicesService", "跳过设备: "+udid+" -> "+err.Error())
			if deviceHint == "" {
				errMsg := strings.ToLower(err.Error())
				switch {
				case strings.Contains(errMsg, "devicelocked"), strings.Contains(errMsg, "device is locked"):
					deviceHint = "设备已锁定，请解锁 iPhone 或 iPad 后重新刷新"
				case strings.Contains(errMsg, "pairingdialogresponsepending"):
					deviceHint = "设备正在等待配对确认，请解锁设备并选择“信任此电脑”"
				case strings.Contains(errMsg, "invalidhostid"), strings.Contains(errMsg, "invalid host id"),
					strings.Contains(errMsg, "invalidpairrecord"), strings.Contains(errMsg, "userdeniedpairing"):
					deviceHint = "设备配对失败，请解锁设备、重新连接 USB 并选择“信任此电脑”"
				}
				if deviceHint != "" {
					Log.Warn("DevicesService", deviceHint+"；Error: "+err.Error())
				}
			}
			continue
		}

		devices = append(devices, info)
	}

	if len(devices) == 0 && lastDeviceErr != nil {
		if deviceHint != "" {
			return nil, fmt.Errorf("%s", deviceHint)
		}
		err := fmt.Errorf("读取已连接设备信息失败: %w", lastDeviceErr)
		Log.Error("DevicesService", err.Error())
		return nil, err
	}

	Log.Info("DevicesService", fmt.Sprintf("找到 %d 个可连接设备", len(devices)))
	return devices, nil
}

// SelectDevice 选择设备
func (d *DevicesService) SelectDevice(udid string) error {
	if d.runningService == nil {
		return fmt.Errorf("跑步服务尚未初始化")
	}
	return d.runningService.withIdleDeviceOperation(func() error {
		return d.selectDevice(udid)
	})
}

func (d *DevicesService) selectDevice(udid string) error {
	Log.Info("DevicesService", "选择设备: "+udid)
	if udid == "" {
		return fmt.Errorf("未选择设备")
	}

	_, version, err := GetDeviceAndVersion(udid)
	if err != nil {
		Log.Error("DevicesService", "选择设备 "+udid+" 失败: "+err.Error())
		return err
	}

	// 先检测 iOS 版本
	if version.Major() >= 17 {
		Log.Info("DevicesService", "检测到 iOS 17+，启用隧道服务")
		if err := ensureTunnelReady(udid, version); err != nil {
			message := err.Error()
			if !strings.HasPrefix(message, "已取消管理员权限请求") && !strings.HasPrefix(message, "正在以管理员身份重启") {
				Log.Error("TunnelService", message)
			}
			return err
		}
	}

	if err := MountImage(udid); err != nil {
		if strings.Contains(err.Error(), "DeviceLocked") {
			Log.Error("ImageService", "设备已锁定，请解锁 iPhone 或 iPad 后重试；底层错误: "+err.Error())
		} else {
			Log.Error("ImageService", err.Error())
		}
		return err
	}
	d.mu.Lock()
	d.selectedUDID = udid
	d.mu.Unlock()

	Log.Info("DevicesService", fmt.Sprintf("挂载开发者镜像完成: %s", udid))
	return nil
}

// GetSelectedDevice 获取已选设备信息
func (d *DevicesService) GetSelectedDevice() (*DeviceInfo, error) {
	d.mu.RLock()
	udid := d.selectedUDID
	if udid == "" {
		d.mu.RUnlock()
		return nil, fmt.Errorf("未选择设备")
	}

	d.mu.RUnlock()

	info, err := GetDeviceInfo(udid)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

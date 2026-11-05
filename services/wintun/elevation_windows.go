//go:build windows

package wintun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

const elevationParentArg = "--iosghostrun-elevation-parent="

// RequestElevation 使用 UAC 启动新进程，旧进程退出后新进程再初始化
func RequestElevation(confirm func() bool) (bool, error) {
	if windows.GetCurrentProcessToken().IsElevated() {
		return false, nil
	}
	if confirm == nil {
		return false, fmt.Errorf("无法显示管理员权限确认提示")
	}
	if !confirm() {
		return false, fmt.Errorf("已取消管理员权限请求")
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("获取程序路径失败: %w", err)
	}
	args := make([]string, 0, len(os.Args))
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, elevationParentArg) {
			args = append(args, arg)
		}
	}
	args = append(args, elevationParentArg+strconv.Itoa(os.Getpid()))
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return false, fmt.Errorf("程序路径无效: %w", err)
	}
	params, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return false, fmt.Errorf("程序启动参数无效: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = filepath.Dir(exe)
	}
	dir, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		return false, fmt.Errorf("工作目录无效: %w", err)
	}
	if err := windows.ShellExecute(0, verb, file, params, dir, windows.SW_SHOWNORMAL); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return false, fmt.Errorf("已取消管理员权限请求: %w", err)
		}
		return false, fmt.Errorf("请求管理员权限失败: %w", err)
	}
	return true, nil
}

// WaitForElevationParent 在数据目录和单实例锁初始化前等待旧进程退出
func WaitForElevationParent() error {
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, elevationParentArg) {
			continue
		}
		pid, err := strconv.ParseUint(strings.TrimPrefix(arg, elevationParentArg), 10, 32)
		if err != nil || pid == 0 {
			return fmt.Errorf("提权重启参数无效")
		}
		process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil // 旧进程已退出
		}
		if err != nil {
			return fmt.Errorf("等待旧进程退出失败: %w", err)
		}
		defer windows.CloseHandle(process)
		result, err := windows.WaitForSingleObject(process, 120000)
		if err != nil {
			return fmt.Errorf("等待旧进程退出失败: %w", err)
		}
		if result != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("等待旧进程退出超时")
		}
		return nil
	}
	return nil
}

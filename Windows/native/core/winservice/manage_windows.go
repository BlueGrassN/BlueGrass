//go:build windows

package winservice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bluegrass/native/core/serviceipc"
	"bluegrass/native/core/tunengine"
	"bluegrass/native/core/winprotect"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func Install() error {
	root, _, err := serviceipc.Installation()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(exe), filepath.Join(root, "BlueGrass.Service.exe")) {
		return errors.New("INVALID_SERVICE_PATH")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	config := mgr.Config{DisplayName: "BlueGrass 网络保护服务", Description: "BlueGrass 全局接管与断线保护", StartType: mgr.StartAutomatic, ServiceStartName: "LocalSystem", SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED}
	s, err := m.OpenService(Name)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		s, err = m.CreateService(Name, exe, config, "run")
	} else if err == nil {
		existing, readErr := s.Config()
		if readErr != nil {
			s.Close()
			return readErr
		}
		expected := `"` + exe + `" run`
		if !strings.EqualFold(existing.BinaryPathName, expected) {
			s.Close()
			return errors.New("SERVICE_OWNERSHIP_MISMATCH")
		}
		config.BinaryPathName = expected
		err = s.UpdateConfig(config)
	}
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: time.Second}, {Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 30 * time.Second}}, 86400); err != nil {
		return err
	}
	return s.SetRecoveryActionsOnNonCrashFailures(true)
}

func ownedService() (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	s, err := m.OpenService(Name)
	if err != nil {
		m.Disconnect()
		return nil, nil, err
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		s.Close()
		m.Disconnect()
		return nil, nil, err
	}
	config, err := s.Config()
	if err != nil || !strings.EqualFold(config.BinaryPathName, `"`+filepath.Join(programs, "BlueGrass", "BlueGrass.Service.exe")+`" run`) {
		s.Close()
		m.Disconnect()
		return nil, nil, errors.New("SERVICE_OWNERSHIP_MISMATCH")
	}
	return m, s, nil
}

func stop(s *mgr.Service) error {
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	_, err = s.Control(svc.Stop)
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status, err = s.Query()
		if err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("SERVICE_STOP_TIMEOUT")
}

// Maintenance stops forwarding through the SCM path. The stopped service
// leaves protection armed, so replacing binaries cannot silently release it.
func Maintenance() error {
	m, s, err := ownedService()
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	return stop(s)
}
func Start() error {
	m, s, err := ownedService()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Running {
		return nil
	}
	return s.Start()
}
func Uninstall() error {
	m, s, err := ownedService()
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return repairOwnedNetwork(tunengine.RepairAdapter, ClearOwnedCache, winprotect.Repair)
	}
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err = stop(s); err != nil {
		return err
	}
	if err = repairOwnedNetwork(tunengine.RepairAdapter, ClearOwnedCache, winprotect.Repair); err != nil {
		return err
	}
	return s.Delete()
}
func Status() (map[string]any, error) {
	m, s, err := ownedService()
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return map[string]any{"installed": false}, nil
	}
	if err != nil {
		return nil, err
	}
	defer m.Disconnect()
	defer s.Close()
	state, err := s.Query()
	if err != nil {
		return nil, err
	}
	return map[string]any{"installed": true, "running": state.State == svc.Running, "processId": state.ProcessId}, nil
}

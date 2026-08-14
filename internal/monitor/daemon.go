package monitor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/data-cleaner/internal/config"
)

const (
	defaultCacheDir = ".data-cleaner-cache/"
	pidFileName     = "monitor.pid"
	logFileName     = "monitor.log"
)

func CacheBaseDir(cfg *config.Config) string {
	if cfg.Cache.Directory != "" {
		return cfg.Cache.Directory
	}
	return defaultCacheDir
}

func pidPath(cfg *config.Config) string {
	return filepath.Join(CacheBaseDir(cfg), pidFileName)
}

func logPath(cfg *config.Config) string {
	return filepath.Join(CacheBaseDir(cfg), logFileName)
}

func RunForeground(cfg *config.Config) error {
	scheduler := NewScheduler(cfg, CacheBaseDir(cfg))

	if err := scheduler.Start(); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	log.Println("[monitor] received shutdown signal, waiting for tasks to complete...")

	scheduler.GracefulStop(30 * time.Second)
	return nil
}

func RunDaemon(cfg *config.Config) error {
	pidDir := filepath.Dir(pidPath(cfg))
	os.MkdirAll(pidDir, 0755)

	lp := logPath(cfg)
	lf, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		lf.Close()
		return fmt.Errorf("get executable: %w", err)
	}

	args := []string{"monitor", "start", "-c", getConfigPath()}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		lf.Close()
		return fmt.Errorf("start daemon: %w", err)
	}

	pid := cmd.Process.Pid
	pp := pidPath(cfg)
	if err := os.WriteFile(pp, []byte(strconv.Itoa(pid)), 0644); err != nil {
		log.Printf("[monitor] warning: could not write PID file: %v", err)
	}

	fmt.Printf("Monitor daemon started (PID: %d)\n", pid)
	fmt.Printf("Log file: %s\n", lp)
	fmt.Printf("PID file: %s\n", pp)

	cmd.Process.Release()
	return nil
}

func StopDaemon(cfg *config.Config) error {
	pp := pidPath(cfg)
	data, err := os.ReadFile(pp)
	if err != nil {
		return fmt.Errorf("read PID file: %w (is the daemon running?)", err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return fmt.Errorf("invalid PID: %s", string(data))
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
	}

	fmt.Printf("SIGTERM sent to process %d\n", pid)
	os.Remove(pp)
	return nil
}

func IsDaemonRunning(cfg *config.Config) (bool, int) {
	pp := pidPath(cfg)
	data, err := os.ReadFile(pp)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false, 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pp)
		return false, 0
	}

	return true, pid
}

func RunSingleTask(cfg *config.Config, taskName string) error {
	scheduler := NewScheduler(cfg, CacheBaseDir(cfg))

	result, err := scheduler.RunTask(taskName)
	if err != nil {
		return err
	}

	if result != nil {
		PrintManualScanResult(result)
	}
	return nil
}

var configPath string

func SetConfigPath(p string) {
	configPath = p
}

func getConfigPath() string {
	return configPath
}

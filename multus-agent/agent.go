package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/syslog"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	rsyncPath = "/usr/local/bin/rsync"
)

var (
	sysLog      *syslog.Writer
	syslogDebug bool
)

func debugf(format string, a ...interface{}) {
	if syslogDebug {
		str := fmt.Sprintf(format, a...)
		sysLog.Debug(str)
	}
}

func main() {
	var err error
	sysLog, err = syslog.New(syslog.LOG_EMERG|syslog.LOG_DAEMON, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create syslog logger: %v", err)
		os.Exit(1)
	}
	cfg, err := loadConfig()
	if err != nil {
		sysLog.Err(fmt.Sprintf("%v", err))
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if cfg.Debug {
		syslogDebug = true
	}

	err = os.MkdirAll(cfg.StoragePath, 0700)
	if err != nil {
		sysLog.Err(err.Error())
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defaultArgs := []string{
		"--timeout",
		fmt.Sprintf("%d", cfg.Timeout),
		"--bwlimit",
		cfg.BWLimit,
		"-a",
		"-e",
		"ssh",
	}
	if len(cfg.Includes) == 0 && len(cfg.Excludes) == 0 {
		defaultArgs = append(defaultArgs, []string{
			"--include",
			"**.gz.enc",
			"--include",
			"sig.cache",
			"--exclude",
			"*",
		}...)
	} else {
		for _, inc := range cfg.Includes {
			defaultArgs = append(defaultArgs, []string{"--include", inc}...)
		}
		for _, exc := range cfg.Excludes {
			defaultArgs = append(defaultArgs, []string{"--exclude", exc}...)
		}
	}

	for _, host := range cfg.Hosts {
		sysLog.Info(fmt.Sprintf("syncing %s", host.Hostname))
		storagePath := filepath.Join(cfg.StoragePath, host.Hostname)
		err = os.MkdirAll(storagePath, 0700)
		if err != nil {
			sysLog.Err(fmt.Sprintf("%v - failed to mkdirall %q: %v", host.Hostname, storagePath, err))
			fmt.Fprintf(os.Stderr, "%v -- skipping %v",
				err, host.Hostname)
			continue
		}
		backupPath := filepath.Clean(host.BackupPath)
		args := append(defaultArgs, []string{
			cfg.Login + "@" + host.Hostname + ":" + filepath.Join(backupPath) + string(os.PathSeparator),
			storagePath,
		}...)

		cmd := exec.CommandContext(ctx, rsyncPath, args...)
		stdOutPipe, err := cmd.StdoutPipe()
		if err != nil {
			sysLog.Err(fmt.Sprintf("%v - stdoutpipe: %v", host.Hostname, err))
			fmt.Fprintf(os.Stderr, "ERROR: StdoutPipe: %v\n", err)
			continue
		}
		stdErrPipe, err := cmd.StderrPipe()
		if err != nil {
			sysLog.Err(fmt.Sprintf("%v - stderrpipe: %v", host.Hostname, err))
			fmt.Fprintf(os.Stderr, "ERROR: StderrPipe: %v\n", err)
			continue
		}
		err = cmd.Start()
		if err != nil {
			sysLog.Err(fmt.Sprintf("%v - failed to start: %v", host.Hostname, err))
			fmt.Fprintf(os.Stderr, "ERROR: Start: %v\n", err)
			return
		}
		go func() {
			var buf [1024]byte
			for {
				n, err := stdOutPipe.Read(buf[:])
				if n > 0 {
					//	os.Stdout.Write(buf[0:n])
					//	os.Stdout.Sync()
				}
				if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					sysLog.Err(fmt.Sprintf("%v - stdout Read: %v", host.Hostname, err))
					fmt.Fprintf(os.Stderr, "ERROR: stdout Read: %v\n", err)
					return
				}
			}
		}()
		go func() {
			var buf [1024]byte
			for {
				n, err := stdErrPipe.Read(buf[:])
				if n > 0 {
					//	os.Stderr.Write(buf[0:n])
					//	os.Stderr.Sync()
				}
				if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					sysLog.Err(fmt.Sprintf("%v - stderr Read: %v", host.Hostname, err))
					fmt.Fprintf(os.Stderr, "ERROR: stderr Read: %v\n", err)
					return
				}
			}
		}()

		if err = cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				sysLog.Err(fmt.Sprintf("%v - %v", host.Hostname, err))
				fmt.Fprintf(os.Stderr, "ERROR: Wait: %v\n", err)
			} else {
				switch exitErr.ExitCode() {
				case 12:
					sysLog.Err(fmt.Sprintf("%v - datastream error", host.Hostname))
					fmt.Fprintln(os.Stderr, "datastream error")
				case 23:
					sysLog.Err(fmt.Sprintf("%v - partial transfer error", host.Hostname))
					fmt.Fprintln(os.Stderr, "partial transfer error")
				case 127:
					sysLog.Err(fmt.Sprintf("%v - rsync not found", host.Hostname))
					fmt.Fprintln(os.Stderr, "rsync not found")
				case 255:
					sysLog.Err(fmt.Sprintf("%v - rsync ssh error", host.Hostname))
					fmt.Fprintln(os.Stderr, "rsync ssh error")
				default:

					fmt.Fprintf(os.Stderr, "Unknown error code %v: %v\n", exitErr.ExitCode(), exitErr.String())
				}
			}
		}
		sysLog.Info(fmt.Sprintf("syncing %s successful", host.Hostname))
	}
	err = cleanup(ctx, cfg.StoragePath, cfg.MaxSize, cfg.DryRun)
	if err != nil {
		sysLog.Err(fmt.Sprintf("cleanup: %v", err))
		fmt.Fprintf(os.Stderr, "cleanup: %v\n", err)
	}
}

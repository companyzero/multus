package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"log/syslog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/jrick/ss/keyfile"
	"golang.org/x/term"
)

const FormatVersion = uint16(1)

var (
	syslogDebug bool
	sysLog      *syslog.Writer
)

func usage() {
	fmt.Fprintln(os.Stderr, "backup\nrestore /RESTOREPATH [file] [level]\ncat <inc-file>")
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
		err = fmt.Errorf("loadConfig failed: %v", err)
		log.Printf("%v", err)
		sysLog.Err(err.Error())
		os.Exit(1)
	}
	if cfg.Debug {
		syslogDebug = true
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	if cfg.Profile {
		go func() {
			listenAddr := "localhost:45454"
			sysLog.Info(fmt.Sprintf("Creating profiling server "+
				"listening on %s", listenAddr))
			profileRedirect := http.RedirectHandler("/debug/pprof",
				http.StatusSeeOther)
			http.Handle("/", profileRedirect)
			err := http.ListenAndServe(listenAddr, nil)
			if err != nil {
				sysLog.Err(fmt.Sprintf("%v", err))
				os.Exit(1)
			}
		}()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		for sig := range signals {
			fmt.Fprintf(os.Stderr, "signal received: %v", sig)
			cancel()
		}
	}()

	var gErr error
	switch os.Args[1] {
	case "backup":
		if len(os.Args) != 2 {
			usage()
			os.Exit(1)
		}
		if len(cfg.BackupPath) == 0 {
			fmt.Fprintln(os.Stderr, "backuppath not set")
			os.Exit(1)
		}
		if len(cfg.Backup.Group) == 0 {
			fmt.Fprintln(os.Stderr, "backup group not set")
			os.Exit(1)
		}
		if len(cfg.Backup.Paths) == 0 {
			fmt.Fprintln(os.Stderr, "no paths to backup")
			os.Exit(1)
		}
		if len(cfg.Backup.PubkeyFile) == 0 {
			fmt.Fprintln(os.Stderr, "pubkeyfile not set")
			os.Exit(1)
		}
		pubKeyBytes, err := ioutil.ReadFile(cfg.Backup.PubkeyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		pubKey, err := keyfile.ReadPublicKey(bytes.NewReader(pubKeyBytes))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		gErr = backup(ctx, pubKey, cfg)
	case "cat":
		if len(os.Args) < 3 {
			usage()
			os.Exit(1)
		}
		if len(cfg.Restore.SecretFile) == 0 {
			fmt.Fprintln(os.Stderr, "secretfile not set")
			os.Exit(1)
		}

		skBytes, err := ioutil.ReadFile(cfg.Restore.SecretFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "%q secret: ", cfg.Restore.SecretFile)
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprint(os.Stderr, "\n")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sk, _, err := keyfile.OpenSecretKey(bytes.NewReader(skBytes), secret)
		zero(secret)
		zero(skBytes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		gErr = cat(ctx, sk, os.Args[2])
	case "restore":
		if len(os.Args) < 3 {
			usage()
			os.Exit(1)
		}
		destDir := filepath.Clean(os.Args[2])

		var fileRegexp *regexp.Regexp
		if len(os.Args) > 3 {
			fileRegexp, err = regexp.Compile(os.Args[3])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}

		ii := int32(-1)
		if len(os.Args) > 4 {
			i, err := strconv.ParseUint(os.Args[4], 10, 16)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			ii = int32(i)
		}

		if len(cfg.Restore.SecretFile) == 0 {
			fmt.Fprintln(os.Stderr, "secretfile not set")
			os.Exit(1)
		}
		skBytes, err := ioutil.ReadFile(cfg.Restore.SecretFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "%q secret: ", cfg.Restore.SecretFile)
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprint(os.Stderr, "\n")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sk, _, err := keyfile.OpenSecretKey(bytes.NewReader(skBytes), secret)
		zero(secret)
		zero(skBytes)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		gErr = restore(ctx, sk, cfg.BackupPath, destDir, fileRegexp, ii)
	default:
		usage()
		os.Exit(1)
	}
	if gErr != nil {
		sysLog.Err(gErr.Error())
		os.Exit(1)
	}
}

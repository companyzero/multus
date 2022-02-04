package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/jrick/ss/stream"
	"github.com/smtc/rsync"
)

const (
	memoryLimit = 1024 * 1024 * 10
)

func lookupGroup(groupName string) (int, error) {
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return -1, err
	}
	gid, err := strconv.ParseInt(group.Gid, 10, 64)
	if err != nil {
		return -1, err
	}
	return int(gid), nil
}

func debugf(format string, a ...interface{}) {
	if syslogDebug {
		str := fmt.Sprintf(format, a...)
		sysLog.Debug(str)
	}
}

func removeOld(destDir string, dryRun bool) {
	files, err := ioutil.ReadDir(destDir)
	if err != nil {
		panic(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".gz.enc") {
			continue
		}
		filePath := filepath.Join(destDir, file.Name())
		if dryRun {
			debugf("deleting %s (dryrun)", filePath)
		} else {
			debugf("deleting %s", filePath)
			err := os.Remove(filePath)
			if err != nil {
				sysLog.Err(fmt.Sprintf("failed to delete %v: %v", filePath, err))
				continue
			}
		}
	}
}

func backup(ctx context.Context, pubKey *stream.PublicKey, cfg *config) error {
	sysLog.Info("starting backup")
	destDir := filepath.Clean(cfg.BackupPath)
	destDirAbs, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	gid, err := lookupGroup(cfg.Backup.Group)
	if err != nil {
		return err
	}
	uid := os.Geteuid()

	err = os.MkdirAll(destDir, 0750)
	if err != nil {
		return err
	}
	err = os.Chown(destDir, uid, gid)
	if err != nil {
		return fmt.Errorf("failed to chown %q: %w", destDir, err)
	}

	sigFile := filepath.Join(destDir, "sig.cache")
	existingSC, err := LoadSignatureCache(sigFile)
	if err != nil && !os.IsNotExist(err) {
		sysLog.Err(fmt.Sprintf("failed to load signature file %q: %v", sigFile, err))
		existingSC = nil
	}

	var sc *SignatureCache
	if existingSC == nil || existingSC.Instance()+1 > cfg.Backup.MaxIntervals {
		existingSC = nil
		sc, err = NewSignatureCache(filepath.Join(destDir, "sig.cache.inprogress"), time.Now(), 0)
	} else {
		sc, err = NewSignatureCache(filepath.Join(destDir, "sig.cache.inprogress"), existingSC.timeStamp, existingSC.Instance()+1)
	}
	if err != nil {
		return fmt.Errorf("failed to create new signature cache: %w", err)
	}

	pathsToCheck := existingSC.Paths()

	if sc.instance == 0 {
		removeOld(destDir, cfg.DryRun)
	}

	debugf("RUNNING LEVEL %d (%v)", sc.instance, sc.timeStamp)

	snap, err := NewSnapshot(ctx, pubKey, uid, gid, cfg.Backup.GZLevel, destDir, sc.hostname, sc.timeStamp, sc.instance, sc.version)
	if err != nil {
		return err
	}

	readBuffer := new(bytes.Reader)
	currentSig := new(bytes.Buffer)
	thisSig := new(bytes.Buffer)
	delta := new(bytes.Buffer)

	startTime := time.Now()
	filesExcluded := int32(0)

	var srcFD *os.File
	for _, sourceDir := range cfg.Backup.Paths {
		err = filepath.Walk(sourceDir, func(srcRelPath string, info os.FileInfo, err error) error {
			if delta.Cap() > memoryLimit {
				delta = new(bytes.Buffer)
				debug.FreeOSMemory()
			}
			if thisSig.Cap() > memoryLimit {
				thisSig = new(bytes.Buffer)
				debug.FreeOSMemory()
			}
			if currentSig.Cap() > memoryLimit {
				currentSig = new(bytes.Buffer)
				debug.FreeOSMemory()
			}

			if err != nil {
				sysLog.Err(fmt.Sprintf("Walk: %v", err))
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}

			srcPath, err := filepath.Abs(srcRelPath)
			if err != nil {
				return err
			}

			// do not backup destination directory
			if strings.HasPrefix(srcPath, destDirAbs) {
				return nil
			}

			for _, exclude := range cfg.Backup.rExcludes {
				if exclude.MatchString(srcPath) {
					filesExcluded++
					debugf("%q: excluding", srcPath)
					return nil
				}
			}

			MD, err := NewMetadata(srcPath)
			if err != nil {
				return err
			}

			currentSig.Reset()
			err = existingSC.Get(currentSig, srcPath)
			if err != nil {
				return err
			}

			thisSig.Reset()
			fileMode := os.FileMode(MD.Attribs.Mode)
			switch {
			case isSocket(fileMode):
				debugf("skipping socket file: %v", srcPath)
				return nil
			case isCharDevice(fileMode):
				fallthrough
			case isDevice(fileMode):
				fallthrough
			case isNamedPipe(fileMode):
				fallthrough
			case isDir(fileMode):
				err = GenSignature(thisSig, MD, nil, 0)
				if err != nil {
					return err
				}
				if !bytes.Equal(currentSig.Bytes(), thisSig.Bytes()) {
					if currentSig.Len() != 0 {
						debugf("%q changed", srcPath)
					} else {
						debugf("%q new file", srcPath)
					}
					err = snap.Add(MD, nil, 0)
					if err != nil {
						return err
					}
					err = sc.Add(srcPath, thisSig.Bytes())
					if err != nil {
						return err
					}
				} else {
					debugf("%q no change", srcPath)
					err = sc.Add(srcPath, currentSig.Bytes())
					if err != nil {
						return err
					}
				}
				delete(pathsToCheck, srcPath)
				return nil
			case isSymlink(fileMode):
				dest, err := os.Readlink(srcPath)
				if err != nil {
					return err
				}
				dataReader := bytes.NewReader([]byte(dest))
				err = GenSignature(thisSig, MD, dataReader, int64(dataReader.Len()))
				if err != nil {
					return err
				}
				if !bytes.Equal(currentSig.Bytes(), thisSig.Bytes()) {
					if currentSig.Len() != 0 {
						debugf("%q changed", srcPath)

						delta.Reset()
						readBuffer.Reset(currentSig.Bytes())
						err = rsync.GenDelta(readBuffer, dataReader, int64(dataReader.Len()), delta)
						if err != nil {
							return err
						}
						dataReader.Reset(delta.Bytes())
					} else {
						debugf("%q new file", srcPath)
					}
					err = snap.Add(MD, dataReader, int64(dataReader.Len()))
					if err != nil {
						return err
					}
					err = sc.Add(srcPath, thisSig.Bytes())
					if err != nil {
						return err
					}
				} else {
					debugf("%q: no change", srcPath)
					err = sc.Add(srcPath, currentSig.Bytes())
					if err != nil {
						return err
					}
				}
				delete(pathsToCheck, srcPath)
				return nil
			default:
				srcFD, err = os.Open(srcPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Open: %v\n", err)
					return nil
				}
				err = GenSignature(thisSig, MD, srcFD, info.Size())
				if err != nil {
					srcFD.Close()
					return err
				}
				if !bytes.Equal(currentSig.Bytes(), thisSig.Bytes()) {
					if currentSig.Len() != 0 {
						debugf("%q: changed", srcPath)
						readBuffer.Reset(currentSig.Bytes())

						if info.Size() > memoryLimit*10 {
							tmpFile, err := os.CreateTemp(cfg.BackupPath, "delta")
							if err != nil {
								srcFD.Close()
								return err
							}
							err = rsync.GenDelta(readBuffer, srcFD, info.Size(), tmpFile)
							if err != nil {
								tmpFile.Close()
								os.Remove(tmpFile.Name())
								srcFD.Close()
								return err
							}
							if _, err = tmpFile.Seek(0, 0); err != nil {
								tmpFile.Close()
								os.Remove(tmpFile.Name())
								srcFD.Close()
								return err
							}
							tmpFileInfo, err := tmpFile.Stat()
							if err != nil {
								tmpFile.Close()
								os.Remove(tmpFile.Name())
								srcFD.Close()
								return err
							}
							err = snap.Add(MD, tmpFile, tmpFileInfo.Size())
							if err != nil {
								tmpFile.Close()
								os.Remove(tmpFile.Name())
								srcFD.Close()
								return err
							}
							tmpFile.Close()
							os.Remove(tmpFile.Name())
						} else {
							delta.Reset()
							readBuffer.Reset(currentSig.Bytes())
							err = rsync.GenDelta(readBuffer, srcFD, info.Size(), delta)
							if err != nil {
								srcFD.Close()
								return err
							}
							readBuffer.Reset(delta.Bytes())

							err = snap.Add(MD, readBuffer, int64(readBuffer.Len()))
							readBuffer.Reset(nil)
						}
					} else {
						debugf("%q new file", srcPath)
						st, err := srcFD.Stat()
						if err != nil {
							srcFD.Close()
							return err
						}
						err = snap.Add(MD, srcFD, st.Size())
						if err != nil {
							srcFD.Close()
							return err
						}
					}
					if err != nil {
						srcFD.Close()
						return err
					}
					err = sc.Add(srcPath, thisSig.Bytes())
					if err != nil {
						srcFD.Close()
						return err
					}
				} else {
					debugf("%q: no change", srcPath)
					err = sc.Add(srcPath, currentSig.Bytes())
					if err != nil {
						srcFD.Close()
						return err
					}
				}
				srcFD.Close()
				delete(pathsToCheck, srcPath)
				return nil
			}
		})
		if err != nil {
			snap.Close()
			os.Remove(snap.Name())
			return fmt.Errorf("error walking the path %q: %v", sourceDir, err)
		}
	}

	// handle deleted files
	for deletedFilePath := range pathsToCheck {
		debugf("%q: deleted", deletedFilePath)
		err = snap.Add(&Metadata{Path: deletedFilePath, Attribs: FileAttributes{}}, nil, 0)
		if err != nil {
			snap.Close()
			os.Remove(snap.Name())
			return err
		}
	}

	if err = snap.Close(); err != nil {
		os.Remove(snap.Name())
		return err
	}

	if err = sc.Close(); err != nil {
		return err
	}
	if err = existingSC.Close(); err != nil {
		return err
	}
	if err = os.Rename(sc.fd.Name(), sigFile); err != nil {
		return err
	}
	err = os.Chown(sigFile, uid, gid)
	if err != nil {
		sysLog.Err(fmt.Sprintf("failed to chown signature file %q: %v", sigFile, err))
	}

	sysLog.Info(fmt.Sprintf("completed: duration:%v bytes written:%d files-skipped:%d",
		time.Since(startTime), snap.BytesWritten(), filesExcluded))
	return nil
}
